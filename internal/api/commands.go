package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"claude-remote-server/internal/models"
)

// Client→server command path (2026-09-02).
//
// The phone/web app sends {"type":"client_command","data":{"op":…}} frames
// over /ws. In relay mode this is the ONLY path — the relay forwards frames
// verbatim in both directions, but the phone has no HTTP route to the
// desktop. The same dispatcher also backs the REST endpoints for LAN/curl
// use and tests.

// clientCommand is the decoded `data` payload of a client_command frame.
type clientCommand struct {
	Op              string                 `json:"op"`
	DecisionID      string                 `json:"decision_id,omitempty"`
	Action          string                 `json:"action,omitempty"`
	Answer          map[string]string      `json:"answer,omitempty"`
	Notes           string                 `json:"notes,omitempty"`
	SuggIdx         int                    `json:"suggestion_index,omitempty"`
	SessionID       string                 `json:"session_id,omitempty"`
	Text            string                 `json:"text,omitempty"`
	AfterID         uint64                 `json:"after_id,omitempty"`
	ApprovalWaitS   *int                   `json:"approval_wait_s,omitempty"`
	LogAutoClearMin *int                   `json:"log_autoclear_min,omitempty"`
	Permissions     map[string]interface{} `json:"permissions,omitempty"`
}

// handleClientFrameRaw parses one raw client frame and dispatches it.
// Returns the HTTP-style status for REST callers (200/400/404/500). Called
// synchronously from the /ws read loop and the relay reader so command ORDER
// is preserved (a one-goroutine-per-frame dispatch could reorder prompts).
func (s *Server) handleClientFrameRaw(body []byte) int {
	var frame struct {
		Type string        `json:"type"`
		Data clientCommand `json:"data"`
	}
	if err := json.Unmarshal(body, &frame); err != nil || frame.Type != "client_command" {
		return http.StatusBadRequest
	}
	code := s.dispatchCommand(frame.Data)
	if code != http.StatusOK {
		// Surface failures to the phone (optimistic UI can then refetch
		// the truth) — a rejected permissions_set would otherwise leave the
		// local list looking applied.
		s.store.Publish(models.WebSocketMessage{
			Type:      "command_error",
			Data:      map[string]interface{}{"op": frame.Data.Op, "status": code},
			Timestamp: time.Now(),
		})
	}
	return code
}

func (s *Server) dispatchCommand(c clientCommand) int {
	switch c.Op {
	case "decision":
		res := models.DecisionResolution{
			Action: c.Action, Answer: c.Answer, Notes: c.Notes, By: "phone",
		}
		if c.Action == "always_allow" {
			if d := s.findDecision(c.DecisionID); d != nil && c.SuggIdx >= 0 &&
				c.SuggIdx < len(d.Suggestions) {
				res.Suggestion = d.Suggestions[c.SuggIdx]
			} else {
				res.Action = "allow" // no valid suggestion → plain allow
			}
		}
		if !s.store.ResolveDecision(c.DecisionID, res) {
			return http.StatusNotFound
		}
		return http.StatusOK
	case "prompt":
		if c.SessionID == "" || c.Text == "" {
			return http.StatusBadRequest
		}
		s.store.EnqueuePrompt(c.SessionID, c.Text)
		return http.StatusOK
	case "clear_logs":
		s.store.ClearLogs()
		return http.StatusOK
	case "app_settings":
		if c.ApprovalWaitS != nil {
			clamped := clampApprovalWait(*c.ApprovalWaitS)
			c.ApprovalWaitS = &clamped
		}
		if c.LogAutoClearMin != nil {
			clamped := clampAutoClear(*c.LogAutoClearMin)
			c.LogAutoClearMin = &clamped
		}
		// Atomic merge under one lock — the previous unlocked
		// read-modify-write could lose a concurrent update.
		s.persistAppSettings(s.store.UpdateAppSettings(c.ApprovalWaitS, c.LogAutoClearMin))
		return http.StatusOK
	case "permissions_get":
		perms, err := s.permissionsGet()
		if err != nil {
			return http.StatusInternalServerError
		}
		s.store.Publish(models.WebSocketMessage{
			Type: "permissions", Data: map[string]interface{}{"permissions": perms}, Timestamp: time.Now(),
		})
		return http.StatusOK
	case "permissions_set":
		if c.Permissions == nil {
			return http.StatusBadRequest
		}
		if err := s.permissionsSet(c.Permissions); err != nil {
			return http.StatusBadRequest
		}
		return http.StatusOK
	case "process_sync":
		sid := c.SessionID
		if sid == "" {
			if snap := s.store.GetSnapshot(); snap.ActiveSession != nil {
				sid = snap.ActiveSession.ID
			}
		}
		s.store.Publish(models.WebSocketMessage{
			Type:      "process_sync",
			Data:      map[string]interface{}{"events": s.store.ProcessEvents(sid, c.AfterID, 100)},
			Timestamp: time.Now(),
		})
		return http.StatusOK
	default:
		return http.StatusBadRequest
	}
}

func (s *Server) findDecision(id string) *models.PendingDecision {
	for _, d := range s.store.PendingDecisions() {
		if d.ID == id {
			return d
		}
	}
	return nil
}

func clampApprovalWait(v int) int {
	if v < 15 {
		return 15
	}
	if v > 110 {
		return 110
	}
	return v
}

func clampAutoClear(v int) int {
	switch v {
	case 5, 15, 30:
		return v
	default:
		return 0 // off
	}
}

// --- REST mirrors (LAN + tests; the phone uses the WS path) ---

func (s *Server) handleDecisionPost(w http.ResponseWriter, r *http.Request) {
	var c clientCommand
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	c.Op = "decision"
	if code := s.dispatchCommand(c); code == http.StatusNotFound {
		http.Error(w, `{"error":"unknown decision"}`, http.StatusNotFound)
		return
	} else if code != http.StatusOK {
		http.Error(w, `{"error":"bad request"}`, code)
		return
	}
	writeOK(w)
}

func (s *Server) handlePromptPost(w http.ResponseWriter, r *http.Request) {
	var c clientCommand
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil || c.SessionID == "" || c.Text == "" {
		http.Error(w, `{"error":"session_id and text required"}`, http.StatusBadRequest)
		return
	}
	c.Op = "prompt"
	if code := s.dispatchCommand(c); code != http.StatusOK {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	writeOK(w)
}

func (s *Server) handleProcessGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sid := q.Get("session_id")
	var after uint64
	if v := q.Get("after"); v != "" {
		fmt.Sscanf(v, "%d", &after)
	}
	writeJSON(w, map[string]interface{}{"events": s.store.ProcessEvents(sid, after, 200)})
}

func (s *Server) handleClearLogsPost(w http.ResponseWriter, r *http.Request) {
	s.store.ClearLogs()
	writeOK(w)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.store.AppSettings())
	case http.MethodPost:
		var in models.AppSettings
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
			return
		}
		in.ApprovalWaitS = clampApprovalWait(in.ApprovalWaitS)
		in.LogAutoClearMin = clampAutoClear(in.LogAutoClearMin)
		s.store.SetAppSettings(in)
		s.persistAppSettings(in)
		writeJSON(w, in)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
