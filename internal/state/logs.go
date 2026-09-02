package state

import (
	"strings"
	"time"

	"claude-remote-server/internal/models"
)

// Log clearing + auto-clear (2026-09-02).
//
// "Logs" are the three activity surfaces the app shows: notifications,
// each session's RecentLogs, and the process-event feed. ClearLogs wipes
// all three (manual clear). The auto-clear window (5/15/30 minutes, driven
// by AppSettings.LogAutoClearMin, 0 = off) drops ENTRIES older than the
// window from the 1s liveness ticker, so the app's log views only ever
// show the configured window of activity.

// ClearLogs wipes notifications, every session's RecentLogs, and process
// events, then broadcasts logs_cleared.
func (s *Store) ClearLogs() {
	s.mu.Lock()
	s.notifications = nil
	for _, sess := range s.sessions {
		sess.RecentLogs = nil
		sess.ProcessEvents = nil
	}
	s.mu.Unlock()
	s.broadcast(models.WebSocketMessage{Type: "logs_cleared", Data: map[string]string{}, Timestamp: time.Now()})
}

// parseLogTime extracts the "[15:04:05]" prefix RecentLogs entries carry
// (every writer stamps seconds via Format("15:04:05")). Same-day best-effort:
// a parsed clock time more than 12h in the future means the entry was written
// before midnight and belongs to yesterday.
func parseLogTime(entry string, now time.Time) (time.Time, bool) {
	if !strings.HasPrefix(entry, "[") {
		return time.Time{}, false
	}
	end := strings.Index(entry, "]")
	if end < 0 {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("15:04:05", entry[1:end], now.Location())
	if err != nil {
		return time.Time{}, false
	}
	stamped := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), t.Second(), 0, now.Location())
	if stamped.After(now.Add(12 * time.Hour)) {
		stamped = stamped.AddDate(0, 0, -1)
	}
	return stamped, true
}

// purgeStaleLogs enforces the auto-clear window. Called from the 1s liveness
// ticker; a no-op while the window is 0 (off).
//
// Purged slices are REPLACED with freshly allocated ones — never compacted
// in place (`x[:0]` + append): snapshots and session_update frames hand
// these slices to JSON encoders outside the lock, and in-place compaction
// would shift elements under the reader (torn reads).
func (s *Store) purgeStaleLogs(now time.Time) {
	mins := s.AppSettings().LogAutoClearMin
	if mins == 0 {
		return
	}
	cutoff := now.Add(-time.Duration(mins) * time.Minute)

	s.mu.Lock()
	var keptNotifs []*models.AppNotification
	for _, n := range s.notifications {
		if n.Timestamp.After(cutoff) {
			keptNotifs = append(keptNotifs, n)
		}
	}
	s.notifications = keptNotifs
	for _, sess := range s.sessions {
		if len(sess.RecentLogs) > 0 {
			var logs []string
			for _, l := range sess.RecentLogs {
				// Keep unparseable entries — pruning must never eat data it
				// cannot date.
				if t, ok := parseLogTime(l, now); !ok || t.After(cutoff) {
					logs = append(logs, l)
				}
			}
			sess.RecentLogs = logs
		}
		if len(sess.ProcessEvents) > 0 {
			var evts []models.ProcessEvent
			for _, e := range sess.ProcessEvents {
				if e.Timestamp.After(cutoff) {
					evts = append(evts, e)
				}
			}
			sess.ProcessEvents = evts
		}
	}
	s.mu.Unlock()
}
