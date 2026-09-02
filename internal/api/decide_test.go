package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"claude-remote-server/internal/models"
	"claude-remote-server/internal/state"
	"claude-remote-server/web"
)

func newDecideServer(t *testing.T) (*Server, *state.Store) {
	t.Helper()
	store := state.NewStore(0, nil)
	return NewServer(0, store, web.EmbeddedFS, nil, testToken), store
}

// resolveFirstPending resolves the first pending decision of the given kind
// from a goroutine, mimicking the phone answering mid-poll.
func resolveFirstPending(t *testing.T, store *state.Store, kind string, res models.DecisionResolution) {
	t.Helper()
	go func() {
		for i := 0; i < 500; i++ {
			time.Sleep(10 * time.Millisecond)
			for _, d := range store.PendingDecisions() {
				if kind == "" || d.Kind == kind {
					store.ResolveDecision(d.ID, res)
					return
				}
			}
		}
	}()
}

func postDecide(t *testing.T, s *Server, payload map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/hook?decide=1&token="+testToken, bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleHookPost(rec, req)
	return rec
}

func TestPermissionRequestLongPollAllow(t *testing.T) {
	s, store := newDecideServer(t)
	resolveFirstPending(t, store, "permission", models.DecisionResolution{Action: "allow", By: "phone"})
	rec := postDecide(t, s, map[string]interface{}{
		"hook_event_name": "PermissionRequest", "session_id": "s1", "permission_mode": "default",
		"tool_name": "Bash", "tool_input": map[string]interface{}{"command": "rm -rf /"},
	})
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad json: %v body=%s", err, rec.Body.String())
	}
	hso, ok := out["hookSpecificOutput"].(map[string]interface{})
	if !ok || hso["hookEventName"] != "PermissionRequest" {
		t.Fatalf("wrong hookSpecificOutput: %v", out)
	}
	dec, _ := hso["decision"].(map[string]interface{})
	if dec["behavior"] != "allow" {
		t.Fatalf("behavior = %v", dec["behavior"])
	}
}

func TestPermissionRequestAlwaysAllowEchoesSuggestion(t *testing.T) {
	s, store := newDecideServer(t)
	sugg := map[string]interface{}{"type": "permission", "permission": "Bash(go test *)"}
	resolveFirstPending(t, store, "permission", models.DecisionResolution{
		Action: "always_allow", By: "phone", Suggestion: sugg,
	})
	rec := postDecide(t, s, map[string]interface{}{
		"hook_event_name": "PermissionRequest", "session_id": "s1", "permission_mode": "default",
		"tool_name":              "Bash",
		"permission_suggestions": []interface{}{sugg},
	})
	var out map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &out)
	hso := out["hookSpecificOutput"].(map[string]interface{})
	dec := hso["decision"].(map[string]interface{})
	if dec["behavior"] != "allow" {
		t.Fatalf("behavior = %v", dec["behavior"])
	}
	perms, ok := dec["updatedPermissions"].([]interface{})
	if !ok || len(perms) != 1 {
		t.Fatalf("updatedPermissions must echo the suggestion: %v", dec)
	}
}

func TestPermissionRequestDeny(t *testing.T) {
	s, store := newDecideServer(t)
	resolveFirstPending(t, store, "permission", models.DecisionResolution{Action: "deny", By: "phone"})
	rec := postDecide(t, s, map[string]interface{}{
		"hook_event_name": "PermissionRequest", "session_id": "s1", "permission_mode": "default",
		"tool_name": "Bash",
	})
	var out map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &out)
	dec := out["hookSpecificOutput"].(map[string]interface{})["decision"].(map[string]interface{})
	if dec["behavior"] != "deny" {
		t.Fatalf("behavior = %v", dec["behavior"])
	}
}

func TestPermissionRequestTimeoutFallsThrough(t *testing.T) {
	s, _ := newDecideServer(t)
	s.approvalWaitOverride = 80 * time.Millisecond // tests shrink the poll window
	start := time.Now()
	rec := postDecide(t, s, map[string]interface{}{
		"hook_event_name": "PermissionRequest", "session_id": "s1", "permission_mode": "default",
		"tool_name": "Bash",
	})
	if time.Since(start) > 5*time.Second {
		t.Fatalf("override wait not honored")
	}
	if strings.TrimSpace(rec.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("timeout must return plain ok, got %s", rec.Body.String())
	}
}

func TestBypassSkipsDecisionWait(t *testing.T) {
	s, _ := newDecideServer(t)
	start := time.Now()
	rec := postDecide(t, s, map[string]interface{}{
		"hook_event_name": "PermissionRequest", "session_id": "s1",
		"permission_mode": "bypassPermissions", "tool_name": "Bash",
	})
	if time.Since(start) > time.Second {
		t.Fatal("bypass must return immediately")
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("bypass must return plain ok, got %s", rec.Body.String())
	}
}

func TestAskUserQuestionAnswerEchoesQuestions(t *testing.T) {
	s, store := newDecideServer(t)
	questions := []interface{}{map[string]interface{}{
		"question": "Which DB?", "header": "DB",
		"options": []interface{}{
			map[string]interface{}{"label": "Postgres", "description": "reliable"},
			map[string]interface{}{"label": "MySQL"},
		},
	}}
	resolveFirstPending(t, store, "question", models.DecisionResolution{
		Action: "answer", By: "phone",
		Answer: map[string]string{"Which DB?": "Postgres"},
	})
	rec := postDecide(t, s, map[string]interface{}{
		"hook_event_name": "PreToolUse", "session_id": "s1", "tool_name": "AskUserQuestion",
		"tool_use_id": "tq1", "tool_input": map[string]interface{}{"questions": questions},
	})
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad json: %v body=%s", err, rec.Body.String())
	}
	hso := out["hookSpecificOutput"].(map[string]interface{})
	if hso["permissionDecision"] != "allow" {
		t.Fatalf("decision = %v", hso["permissionDecision"])
	}
	ui := hso["updatedInput"].(map[string]interface{})
	qs, ok := ui["questions"].([]interface{})
	if !ok || len(qs) != 1 {
		t.Fatalf("updatedInput must echo questions verbatim: %v", ui)
	}
	ans := ui["answers"].(map[string]interface{})
	if ans["Which DB?"] != "Postgres" {
		t.Fatalf("answers = %v", ans)
	}
	// The parked decision must be fully consumed.
	if got := store.PendingDecisions(); len(got) != 0 {
		t.Fatalf("decision should be consumed, got %+v", got)
	}
}

func TestExitPlanModeApproveAndDeny(t *testing.T) {
	s, store := newDecideServer(t)
	resolveFirstPending(t, store, "plan", models.DecisionResolution{Action: "allow", By: "phone"})
	rec := postDecide(t, s, map[string]interface{}{
		"hook_event_name": "PreToolUse", "session_id": "s1", "tool_name": "ExitPlanMode",
		"tool_use_id": "tp1", "tool_input": map[string]interface{}{"plan": "# The Plan"},
	})
	var out map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &out)
	hso := out["hookSpecificOutput"].(map[string]interface{})
	if hso["permissionDecision"] != "allow" {
		t.Fatalf("plan approve = %v body=%s", hso["permissionDecision"], rec.Body.String())
	}

	resolveFirstPending(t, store, "plan", models.DecisionResolution{Action: "deny", By: "phone", Notes: "too risky"})
	rec2 := postDecide(t, s, map[string]interface{}{
		"hook_event_name": "PreToolUse", "session_id": "s1", "tool_name": "ExitPlanMode",
		"tool_use_id": "tp2", "tool_input": map[string]interface{}{"plan": "# The Plan"},
	})
	var out2 map[string]interface{}
	json.Unmarshal(rec2.Body.Bytes(), &out2)
	hso2 := out2["hookSpecificOutput"].(map[string]interface{})
	if hso2["permissionDecision"] != "deny" {
		t.Fatalf("plan deny = %v", hso2["permissionDecision"])
	}
	if !strings.Contains(hso2["permissionDecisionReason"].(string), "too risky") {
		t.Fatalf("deny reason should carry notes: %v", hso2["permissionDecisionReason"])
	}
}

func TestPreToolUseOtherToolsDontWait(t *testing.T) {
	s, _ := newDecideServer(t)
	start := time.Now()
	rec := postDecide(t, s, map[string]interface{}{
		"hook_event_name": "PreToolUse", "session_id": "s1", "tool_name": "Bash",
		"tool_input": map[string]interface{}{"command": "ls"},
	})
	if time.Since(start) > time.Second {
		t.Fatal("non-ask tools must not wait")
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("expected plain ok, got %s", rec.Body.String())
	}
}

func TestStopDeliversQueuedPromptOnce(t *testing.T) {
	s, store := newDecideServer(t)
	store.EnqueuePrompt("s1", "also run the tests")
	rec := postDecide(t, s, map[string]interface{}{
		"hook_event_name": "Stop", "session_id": "s1", "stop_hook_active": false,
	})
	var out map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &out)
	if out["decision"] != "block" || out["reason"] != "also run the tests" {
		t.Fatalf("expected block+reason, got %s", rec.Body.String())
	}
	if store.PromptQueueDepth("s1") != 0 {
		t.Fatal("prompt must be drained")
	}
	// stop_hook_active=true must NOT block.
	store.EnqueuePrompt("s1", "another")
	rec2 := postDecide(t, s, map[string]interface{}{
		"hook_event_name": "Stop", "session_id": "s1", "stop_hook_active": true,
	})
	if strings.Contains(rec2.Body.String(), "block") {
		t.Fatalf("stop_hook_active must not block, got %s", rec2.Body.String())
	}
	if store.PromptQueueDepth("s1") != 1 {
		t.Fatal("prompt must stay queued under stop_hook_active")
	}
	// A later natural Stop (stop_hook_active=false) delivers the held prompt.
	rec3 := postDecide(t, s, map[string]interface{}{
		"hook_event_name": "Stop", "session_id": "s1", "stop_hook_active": false,
	})
	var out3 map[string]interface{}
	json.Unmarshal(rec3.Body.Bytes(), &out3)
	if out3["reason"] != "another" {
		t.Fatalf("held prompt must deliver on next natural stop, got %s", rec3.Body.String())
	}
	if store.PromptQueueDepth("s1") != 0 {
		t.Fatal("queue must be empty after delivery")
	}
}
