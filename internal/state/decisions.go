package state

import (
	"fmt"
	"time"

	"claude-remote-server/internal/models"
)

// decisionEntry pairs a pending decision with its one-shot resolution channel.
type decisionEntry struct {
	decision *models.PendingDecision
	resCh    chan models.DecisionResolution
}

// decisionRegistry holds pending decisions and per-session prompt queues.
// Guarded by the Store mutex (methods take s.mu themselves).
type decisionRegistry struct {
	pending map[string]*decisionEntry
	queues  map[string][]string
}

func newDecisionRegistry() decisionRegistry {
	return decisionRegistry{pending: make(map[string]*decisionEntry), queues: make(map[string][]string)}
}

func (s *Store) nextDecisionID() string {
	return fmt.Sprintf("dec-%d", time.Now().UnixNano())
}

// CreatePendingDecision registers d (assigning ID + expiry) and broadcasts
// decision_pending. Returns the assigned ID.
func (s *Store) CreatePendingDecision(d *models.PendingDecision) string {
	s.mu.Lock()
	d.ID = s.nextDecisionID()
	d.AskedAt = time.Now()
	wait := time.Duration(s.appSettings.ApprovalWaitS) * time.Second
	if wait <= 0 {
		wait = 60 * time.Second
	}
	d.ExpiresAt = time.Now().Add(wait)
	s.decisions.pending[d.ID] = &decisionEntry{decision: d, resCh: make(chan models.DecisionResolution, 1)}
	s.mu.Unlock()
	s.broadcast(models.WebSocketMessage{Type: "decision_pending", Data: d, Timestamp: time.Now()})
	return d.ID
}

// ResolveDecision delivers res to a waiting hook (if any) and broadcasts
// decision_resolved. Returns false for unknown/already-resolved ids.
func (s *Store) ResolveDecision(id string, res models.DecisionResolution) bool {
	s.mu.Lock()
	entry, ok := s.decisions.pending[id]
	if ok {
		delete(s.decisions.pending, id)
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	entry.resCh <- res
	s.broadcast(models.WebSocketMessage{
		Type:      "decision_resolved",
		Data:      map[string]interface{}{"decision_id": id, "action": res.Action, "by": res.By},
		Timestamp: time.Now(),
	})
	return true
}

// WaitForDecision blocks until the decision is resolved or timeout passes.
// Returns (nil, false) for unknown ids or timeout — the caller then falls
// back to Claude Code's normal terminal flow.
func (s *Store) WaitForDecision(id string, timeout time.Duration) (*models.DecisionResolution, bool) {
	s.mu.Lock()
	entry, ok := s.decisions.pending[id]
	s.mu.Unlock()
	if !ok {
		return nil, false
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case res := <-entry.resCh:
		return &res, true
	case <-timer.C:
		// Expire: remove so a late ResolveDecision is a no-op.
		s.mu.Lock()
		delete(s.decisions.pending, id)
		s.mu.Unlock()
		s.broadcast(models.WebSocketMessage{
			Type:      "decision_resolved",
			Data:      map[string]interface{}{"decision_id": id, "action": "expire", "by": "timeout"},
			Timestamp: time.Now(),
		})
		return nil, false
	}
}

// PendingDecisions snapshots the currently parked decisions.
func (s *Store) PendingDecisions() []*models.PendingDecision {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*models.PendingDecision, 0, len(s.decisions.pending))
	for _, e := range s.decisions.pending {
		out = append(out, e.decision)
	}
	return out
}

// EnqueuePrompt appends a mid-task prompt for a session, broadcasting
// prompt_queued. Returns the new queue depth.
func (s *Store) EnqueuePrompt(sessionID, text string) int {
	s.mu.Lock()
	s.decisions.queues[sessionID] = append(s.decisions.queues[sessionID], text)
	depth := len(s.decisions.queues[sessionID])
	if sess, ok := s.sessions[sessionID]; ok {
		sess.PromptQueueDepth = depth
	}
	s.mu.Unlock()
	s.broadcast(models.WebSocketMessage{
		Type:      "prompt_queued",
		Data:      map[string]interface{}{"session_id": sessionID, "depth": depth},
		Timestamp: time.Now(),
	})
	return depth
}

// DrainNextPrompt pops the oldest queued prompt for a session (FIFO).
func (s *Store) DrainNextPrompt(sessionID string) (string, bool) {
	s.mu.Lock()
	q := s.decisions.queues[sessionID]
	if len(q) == 0 {
		s.mu.Unlock()
		return "", false
	}
	head := q[0]
	s.decisions.queues[sessionID] = q[1:]
	if len(s.decisions.queues[sessionID]) == 0 {
		delete(s.decisions.queues, sessionID)
	}
	if sess, ok := s.sessions[sessionID]; ok {
		sess.PromptQueueDepth = len(s.decisions.queues[sessionID])
	}
	s.mu.Unlock()
	return head, true
}

// PromptQueueDepth reports the queued-prompt count for a session.
func (s *Store) PromptQueueDepth(sessionID string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.decisions.queues[sessionID])
}

// AppSettings returns the server's remote-interaction settings.
func (s *Store) AppSettings() models.AppSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.appSettings
}

// SetAppSettings updates remote-interaction settings (clamped by callers).
func (s *Store) SetAppSettings(v models.AppSettings) {
	s.mu.Lock()
	s.appSettings = v
	s.mu.Unlock()
	s.broadcast(models.WebSocketMessage{Type: "app_settings", Data: v, Timestamp: time.Now()})
}
