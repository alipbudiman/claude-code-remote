package state

import (
	"time"

	"claude-remote-server/internal/models"
)

const (
	// MaxEventDetail caps one event's Detail payload.
	MaxEventDetail = 8000
	// processRingCap bounds retained events per session.
	processRingCap = 200
)

// eventSeq is the global process-event sequence; every use is under s.mu.
var eventSeq uint64

// AppendProcessEvent stamps + stores evt in the session's ring and
// broadcasts process_event. Detail is capped at MaxEventDetail.
func (s *Store) AppendProcessEvent(sessionID string, evt *models.ProcessEvent) {
	if len(evt.Detail) > MaxEventDetail {
		const marker = "\n… [truncated]"
		evt.Detail = evt.Detail[:MaxEventDetail-len(marker)] + marker
	}
	s.mu.Lock()
	eventSeq++
	evt.ID = eventSeq
	evt.SessionID = sessionID
	evt.Timestamp = time.Now()
	sess, ok := s.sessions[sessionID]
	if !ok {
		// Events may arrive before any hook for the session (watcher path).
		sess = s.getOrCreateSessionLocked(sessionID, "", "")
	}
	sess.ProcessEvents = append(sess.ProcessEvents, *evt)
	if len(sess.ProcessEvents) > processRingCap {
		sess.ProcessEvents = sess.ProcessEvents[len(sess.ProcessEvents)-processRingCap:]
	}
	s.mu.Unlock()
	s.broadcast(models.WebSocketMessage{Type: "process_event", Data: evt, Timestamp: time.Now()})
}

// ProcessEvents returns up to limit events for a session with ID > afterID.
func (s *Store) ProcessEvents(sessionID string, afterID uint64, limit int) []models.ProcessEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return nil
	}
	var out []models.ProcessEvent
	for i := len(sess.ProcessEvents) - 1; i >= 0 && len(out) < limit; i-- {
		if sess.ProcessEvents[i].ID > afterID {
			out = append([]models.ProcessEvent{sess.ProcessEvents[i]}, out...)
		}
	}
	return out
}
