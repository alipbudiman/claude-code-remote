package state

import (
	"testing"
	"time"

	"claude-remote-server/internal/models"
)

func TestClearLogsWipesEverything(t *testing.T) {
	s := NewStore(0, nil)
	s.AddNotification("s1", "t", "b", "info")
	s.HandleHookEvent(models.HookPayload{HookEventName: "UserPromptSubmit", SessionID: "s1", Prompt: "hi"})
	s.AppendProcessEvent("s1", &models.ProcessEvent{Kind: models.EventText, Title: "x"})
	s.ClearLogs()
	if len(s.GetSnapshot().Notifications) != 0 {
		t.Fatal("notifications not cleared")
	}
	if evts := s.ProcessEvents("s1", 0, 10); len(evts) != 0 {
		t.Fatalf("process events not cleared: %d", len(evts))
	}
	sess := s.GetSnapshot().ActiveSession
	if sess == nil || len(sess.RecentLogs) != 0 {
		t.Fatalf("recent logs not cleared: %+v", sess)
	}
}

func ageAllEvents(s *Store, sessionID string, age time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sess := range s.sessions {
		for i := range sess.ProcessEvents {
			sess.ProcessEvents[i].Timestamp = time.Now().Add(-age)
		}
	}
}

func TestPurgeStaleLogsRespectsWindow(t *testing.T) {
	s := NewStore(0, nil)
	s.SetAppSettings(models.AppSettings{ApprovalWaitS: 60, LogAutoClearMin: 5})
	s.HandleHookEvent(models.HookPayload{HookEventName: "UserPromptSubmit", SessionID: "s1", Prompt: "hi"})
	s.AppendProcessEvent("s1", &models.ProcessEvent{Kind: models.EventText, Title: "old"})
	ageAllEvents(s, "s1", 10*time.Minute)
	s.purgeStaleLogs(time.Now())
	if evts := s.ProcessEvents("s1", 0, 10); len(evts) != 0 {
		t.Fatalf("old events must be purged, got %d", len(evts))
	}

	// Window=0 (off) purges nothing.
	s.SetAppSettings(models.AppSettings{ApprovalWaitS: 60, LogAutoClearMin: 0})
	s.AppendProcessEvent("s1", &models.ProcessEvent{Kind: models.EventText, Title: "keep"})
	ageAllEvents(s, "s1", 10*time.Minute)
	s.purgeStaleLogs(time.Now())
	if evts := s.ProcessEvents("s1", 0, 10); len(evts) == 0 {
		t.Fatal("window=0 must disable purge")
	}
}

func TestPurgeStaleLogsDropsOldNotifications(t *testing.T) {
	s := NewStore(0, nil)
	s.SetAppSettings(models.AppSettings{ApprovalWaitS: 60, LogAutoClearMin: 5})
	s.AddNotification("s1", "old", "body", "info") // timestamped now; age it below
	s.mu.Lock()
	s.notifications[0].Timestamp = time.Now().Add(-10 * time.Minute)
	s.mu.Unlock()
	s.purgeStaleLogs(time.Now())
	if got := s.GetSnapshot().Notifications; len(got) != 0 {
		t.Fatalf("old notification must be purged, got %d", len(got))
	}
}

func TestPurgeStaleLogsPrunesRecentLogs(t *testing.T) {
	s := NewStore(0, nil)
	s.SetAppSettings(models.AppSettings{ApprovalWaitS: 60, LogAutoClearMin: 5})
	s.HandleHookEvent(models.HookPayload{HookEventName: "UserPromptSubmit", SessionID: "s1", Prompt: "hi"})
	sess := s.GetSnapshot().ActiveSession
	if len(sess.RecentLogs) == 0 {
		t.Fatal("expected recent logs entries")
	}
	// Entries carry the writer's [HH:MM:SS] stamp of "now" — inside the 5m
	// window they survive; nothing to age here, so verify the parse itself
	// keeps them.
	s.purgeStaleLogs(time.Now())
	sess = s.GetSnapshot().ActiveSession
	if len(sess.RecentLogs) == 0 {
		t.Fatal("fresh entries must be kept")
	}
}

func TestParseLogTime(t *testing.T) {
	now := time.Date(2026, 9, 2, 15, 0, 0, 0, time.Local)
	// Real writer format: "[15:04:05]" with seconds (store.go appendLog).
	got, ok := parseLogTime("[14:30:05] something happened", now)
	if !ok {
		t.Fatal("expected parse success")
	}
	want := time.Date(2026, 9, 2, 14, 30, 5, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("parsed %v, want %v", got, want)
	}
	// Midnight wrap: a clock time 12h in the future belongs to yesterday.
	late := time.Date(2026, 9, 3, 0, 5, 0, 0, time.Local)
	got, ok = parseLogTime("[23:50:00] entry", late)
	if !ok {
		t.Fatal("expected parse success")
	}
	if got.Day() != 2 {
		t.Fatalf("expected yesterday, got %v", got)
	}
	if _, ok := parseLogTime("no timestamp", now); ok {
		t.Fatal("unparseable entry must return false (and be kept)")
	}
}
