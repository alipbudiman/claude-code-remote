package api

import (
	"encoding/json"
	"net/http"
	"time"

	"claude-remote-server/internal/models"
)

// Decision long-poll for /api/hook?decide=1 (2026-09-02).
//
// Contract summary (verified against https://code.claude.com/docs/en/hooks):
//   - PermissionRequest → hookSpecificOutput.PermissionRequest.decision{behavior:
//     "allow"|"deny"[, updatedPermissions]} (exit code 2 is NOT honored).
//   - PreToolUse(AskUserQuestion) → permissionDecision "allow" + updatedInput
//     echoing the original questions array plus an answers object mapping each
//     question's text to the chosen answer.
//   - PreToolUse(ExitPlanMode) → plain "allow" approves the plan; "deny"+reason
//     rejects it.
//   - Stop → {"decision":"block","reason":"<prompt>"} continues the turn with
//     the reason as Claude's next instruction. Never block when
//     stop_hook_active is true (8-consecutive-block cap protection).
//   - A timed-out command hook does NOT block the tool — the normal terminal
//     flow takes over. That is the fallback everywhere below.

const (
	minApprovalWait = 15 * time.Second
	maxApprovalWait = 110 * time.Second
)

// approvalWait resolves the effective decision wait: the test override, else
// the clamped app setting.
func (s *Server) approvalWait() time.Duration {
	if s.approvalWaitOverride > 0 {
		return s.approvalWaitOverride
	}
	w := time.Duration(s.store.AppSettings().ApprovalWaitS) * time.Second
	if w < minApprovalWait {
		w = minApprovalWait
	}
	if w > maxApprovalWait {
		w = maxApprovalWait
	}
	return w
}

// isAskTool reports whether the tool is the question/plan interactive pair.
func isAskTool(name string) bool {
	return name == "AskUserQuestion" || name == "ask_question" ||
		name == "ExitPlanMode" || name == "exit_plan_mode"
}

// decideHookEvent implements the decision long-poll for ?decide=1 requests.
// The event has ALREADY been fed to the store (state + process feed); this
// only decides the HTTP response body the bridge forwards on stdout.
func (s *Server) decideHookEvent(w http.ResponseWriter, payload models.HookPayload) {
	switch payload.HookEventName {
	case "PermissionRequest":
		if payload.PermissionMode == "bypassPermissions" {
			writeOK(w)
			return
		}
		d := buildPermissionDecision(payload)
		id := s.store.CreatePendingDecision(d)
		s.store.AddNotification(d.SessionID, "⚠️ "+d.Title, d.Question, "permission")
		if res, ok := s.store.WaitForDecision(id, s.approvalWait()); ok {
			w.Header().Set("Content-Type", "application/json")
			dec := map[string]interface{}{"behavior": "allow"}
			if res.Action == "deny" {
				dec = map[string]interface{}{"behavior": "deny"}
			} else if res.Action == "always_allow" && res.Suggestion != nil {
				dec["updatedPermissions"] = []interface{}{res.Suggestion}
			}
			writeJSON(w, map[string]interface{}{
				"hookSpecificOutput": map[string]interface{}{
					"hookEventName": "PermissionRequest",
					"decision":      dec,
				},
			})
			return
		}
		writeOK(w) // timeout → normal terminal prompt

	case "PreToolUse":
		if !isAskTool(payload.ToolName) {
			writeOK(w) // every other tool: feed only, no wait
			return
		}
		d := buildQuestionDecision(payload)
		id := s.store.CreatePendingDecision(d)
		s.store.AddNotification(d.SessionID, "⚠️ "+d.Title, firstNonEmpty(d.Question, d.Title), "permission")
		if res, ok := s.store.WaitForDecision(id, s.approvalWait()); ok {
			w.Header().Set("Content-Type", "application/json")
			writeAskResponse(w, payload, res)
			return
		}
		// Timeout → NO decision: the terminal keeps its normal interactive
		// dialog (questions stay open until answered), same fallback the
		// permission path uses.
		writeOK(w)

	case "Stop":
		if payload.StopHookActive != nil && *payload.StopHookActive {
			writeOK(w)
			return
		}
		if prompt, ok := s.store.DrainNextPrompt(payload.SessionID); ok {
			writeJSON(w, map[string]interface{}{"decision": "block", "reason": prompt})
			return
		}
		writeOK(w)

	default:
		writeOK(w)
	}
}

// writeAskResponse translates a question/plan resolution into PreToolUse JSON.
func writeAskResponse(w http.ResponseWriter, payload models.HookPayload, res *models.DecisionResolution) {
	askQ := payload.ToolName == "AskUserQuestion" || payload.ToolName == "ask_question"
	if askQ && res.Action == "answer" {
		writeJSON(w, map[string]interface{}{
			"hookSpecificOutput": map[string]interface{}{
				"hookEventName":      "PreToolUse",
				"permissionDecision": "allow",
				"updatedInput": map[string]interface{}{
					"questions": rawQuestionsOf(payload.ToolInput),
					"answers":   res.Answer,
				},
			},
		})
		return
	}
	if res.Action == "allow" || res.Action == "always_allow" {
		// Plan approval (ExitPlanMode): plain allow — the plan already
		// lives in the injected tool_input.
		writeJSON(w, map[string]interface{}{
			"hookSpecificOutput": map[string]interface{}{
				"hookEventName":      "PreToolUse",
				"permissionDecision": "allow",
			},
		})
		return
	}
	// deny / dismiss
	reason := "Denied from remote."
	if res.Action == "dismiss" {
		reason = "User dismissed this from the remote app."
	}
	if res.Notes != "" {
		reason += " " + res.Notes
	}
	writeJSON(w, map[string]interface{}{
		"hookSpecificOutput": map[string]interface{}{
			"hookEventName":         "PreToolUse",
			"permissionDecision":    "deny",
			"permissionDecisionReason": reason,
		},
	})
}

// rawQuestionsOf pulls the verbatim questions array from AskUserQuestion input.
func rawQuestionsOf(in map[string]interface{}) interface{} {
	if in == nil {
		return []interface{}{}
	}
	if qs, ok := in["questions"].([]interface{}); ok {
		return qs
	}
	return []interface{}{}
}

func buildPermissionDecision(p models.HookPayload) *models.PendingDecision {
	title := "Permission required: " + p.ToolName
	q := ""
	if p.ToolInput != nil {
		if c, ok := p.ToolInput["command"].(string); ok && c != "" {
			q = c
			title = "Run command"
		} else if f, ok := p.ToolInput["file_path"].(string); ok && f != "" {
			q = f
			title = "Modify file"
		}
	}
	if p.Message != "" {
		q = p.Message
	}
	return &models.PendingDecision{
		SessionID: p.SessionID, Kind: "permission", ToolName: p.ToolName,
		Title: title, Question: q, ToolInput: p.ToolInput,
		Suggestions: p.PermissionSuggestions,
	}
}

// buildQuestionDecision normalizes AskUserQuestion options (objects with
// label/description — current CLI shape — or plain strings) and ExitPlanMode.
func buildQuestionDecision(p models.HookPayload) *models.PendingDecision {
	d := &models.PendingDecision{
		SessionID: p.SessionID, ToolName: p.ToolName, ToolUseID: p.ToolUseID,
		ToolInput: p.ToolInput, Kind: "question",
	}
	if p.ToolName == "ExitPlanMode" || p.ToolName == "exit_plan_mode" {
		d.Kind = "plan"
		d.Title = "Plan ready for review"
		d.Question = "Approve this plan?"
		if p.ToolInput != nil {
			if plan, ok := p.ToolInput["plan"].(string); ok && plan != "" {
				d.Question = plan
			}
		}
		return d
	}
	d.Title = "Claude has a question"
	if p.ToolInput != nil {
		if qs, ok := p.ToolInput["questions"].([]interface{}); ok {
			d.RawQuestions = qs
			for _, q := range qs {
				m, ok := q.(map[string]interface{})
				if !ok {
					continue
				}
				spec := models.QuestionSpec{Options: []models.QuestionOption{}}
				spec.Question, _ = m["question"].(string)
				spec.Header, _ = m["header"].(string)
				spec.MultiSelect, _ = m["multiSelect"].(bool)
				if opts, ok := m["options"].([]interface{}); ok {
					for _, o := range opts {
						switch ov := o.(type) {
						case string:
							spec.Options = append(spec.Options, models.QuestionOption{Label: ov})
						case map[string]interface{}:
							opt := models.QuestionOption{}
							opt.Label, _ = ov["label"].(string)
							opt.Description, _ = ov["description"].(string)
							spec.Options = append(spec.Options, opt)
						}
					}
				}
				d.Questions = append(d.Questions, spec)
			}
		}
		if len(d.Questions) > 0 {
			d.Question = d.Questions[0].Question
		}
		// Legacy single-question shape: {question, options: [string]}.
		if q, ok := p.ToolInput["question"].(string); ok && q != "" && len(d.Questions) == 0 {
			spec := models.QuestionSpec{Question: q, Options: []models.QuestionOption{}}
			if opts, ok := p.ToolInput["options"].([]interface{}); ok {
				for _, o := range opts {
					if sv, ok := o.(string); ok {
						spec.Options = append(spec.Options, models.QuestionOption{Label: sv})
					}
				}
			}
			d.Questions = []models.QuestionSpec{spec}
			d.Question = q
		}
	}
	return d
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func writeOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}
