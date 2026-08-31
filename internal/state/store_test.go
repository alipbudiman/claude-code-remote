package state

import (
	"fmt"
	"testing"
	"time"

	"claude-remote-server/internal/models"
)

// --- test helpers -----------------------------------------------------------

// newTestStore builds a store with a controllable idle timeout so the liveness
// fallback can be exercised deterministically without sleeping.
func newTestStore(idleTimeout time.Duration) *Store {
	return NewStoreWithIdleTimeout(0, nil, idleTimeout)
}

// backdate rewinds a session's LastActivity as if no hook event had arrived
// for the given duration (test is in-package, so the store mutex is visible).
func backdate(s *Store, sessionID string, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[sessionID]; ok {
		sess.LastActivity = time.Now().Add(-d)
	}
}

// forceStatus flips a session status directly (simulates a previously
// mis-idled session without going through the event switch).
func forceStatus(s *Store, sessionID string, status models.SessionStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[sessionID]; ok {
		sess.Status = status
	}
}

func mustSession(t *testing.T, s *Store, sessionID string) *models.Session {
	t.Helper()
	for _, sess := range s.GetSnapshot().Sessions {
		if sess.ID == sessionID {
			return sess
		}
	}
	t.Fatalf("session %q not found in snapshot", sessionID)
	return nil
}

func notifCount(s *Store) int {
	return len(s.GetSnapshot().Notifications)
}

func latestNotif(t *testing.T, s *Store) *models.AppNotification {
	t.Helper()
	notifs := s.GetSnapshot().Notifications
	if len(notifs) == 0 {
		t.Fatal("no notifications recorded")
	}
	return notifs[0]
}

func countNotifType(s *Store, notifType string) int {
	n := 0
	for _, notif := range s.GetSnapshot().Notifications {
		if notif.Type == notifType {
			n++
		}
	}
	return n
}

// --- 1. long-tool race: an in-flight tool blocks the auto-idle fallback ------

func TestLongToolNeverAutoIdled(t *testing.T) {
	s := newTestStore(200 * time.Millisecond)

	s.HandleHookEvent(models.HookPayload{
		HookEventName: "PreToolUse",
		SessionID:     "s1",
		ToolName:      "Bash",
		ToolUseID:     "t1",
	})

	// Far past the idle timeout, as would happen during a long build.
	backdate(s, "s1", 30*time.Minute)
	s.checkLivenessAndAutoIdle()

	sess := mustSession(t, s, "s1")
	if sess.Status != models.StatusActive {
		t.Fatalf("status = %q, want %q (in-flight tool must block auto-idle)", sess.Status, models.StatusActive)
	}
	if _, ok := sess.ActiveToolIDs["t1"]; !ok {
		t.Fatalf("tool t1 missing from ActiveToolIDs: %v", sess.ActiveToolIDs)
	}
	if got := notifCount(s); got != 0 {
		t.Fatalf("notifications = %d, want 0 (no false task_done while a tool runs)", got)
	}
}

// --- 2. PostToolUse restores an idle session; Stop completes the turn --------

func TestPostToolUseRestoresAndStopCompletes(t *testing.T) {
	s := newTestStore(time.Hour)

	s.HandleHookEvent(models.HookPayload{
		HookEventName: "PreToolUse",
		SessionID:     "s1",
		ToolName:      "Bash",
		ToolUseID:     "t1",
	})

	// Simulate the session having been prematurely idled by the old engine.
	forceStatus(s, "s1", models.StatusIdle)

	s.HandleHookEvent(models.HookPayload{
		HookEventName: "PostToolUse",
		SessionID:     "s1",
		ToolUseID:     "t1",
	})

	sess := mustSession(t, s, "s1")
	if sess.Status != models.StatusActive {
		t.Fatalf("status after PostToolUse = %q, want %q (restore)", sess.Status, models.StatusActive)
	}
	if sess.CurrentToolStatus != "Processing results" {
		t.Fatalf("tool status after PostToolUse = %q, want %q", sess.CurrentToolStatus, "Processing results")
	}

	s.HandleHookEvent(models.HookPayload{
		HookEventName: "Stop",
		SessionID:     "s1",
	})

	sess = mustSession(t, s, "s1")
	if sess.Status != models.StatusIdle {
		t.Fatalf("status after Stop = %q, want %q", sess.Status, models.StatusIdle)
	}
	if sess.TurnActive {
		t.Fatal("TurnActive still true after Stop, want false")
	}
	if got := countNotifType(s, "task_done"); got != 1 {
		t.Fatalf("task_done notifications = %d, want exactly 1 (got all: %+v)", got, s.GetSnapshot().Notifications)
	}
}

// --- 3. the asking tool's PostToolUse clears PendingQuestion -----------------

func TestAskUserQuestionClearedByPostToolUse(t *testing.T) {
	s := newTestStore(time.Hour)

	s.HandleHookEvent(models.HookPayload{
		HookEventName: "PreToolUse",
		SessionID:     "s1",
		ToolName:      "AskUserQuestion",
		ToolUseID:     "q1",
	})

	sess := mustSession(t, s, "s1")
	if sess.PendingQuestion == nil {
		t.Fatal("PendingQuestion not set by AskUserQuestion")
	}
	if sess.PendingQuestion.ToolUseID != "q1" {
		t.Fatalf("PendingQuestion.ToolUseID = %q, want %q", sess.PendingQuestion.ToolUseID, "q1")
	}
	if sess.Status != models.StatusWaitingPermission {
		t.Fatalf("status = %q, want %q", sess.Status, models.StatusWaitingPermission)
	}

	// The question was answered at the PC: its PostToolUse must clear it.
	s.HandleHookEvent(models.HookPayload{
		HookEventName: "PostToolUse",
		SessionID:     "s1",
		ToolUseID:     "q1",
	})

	sess = mustSession(t, s, "s1")
	if sess.PendingQuestion != nil {
		t.Fatalf("PendingQuestion still set after PostToolUse(q1): %+v", sess.PendingQuestion)
	}
}

// --- 4. parallel subagents: SubagentStop completes exactly the matched agent -

func TestSubagentStopByAgentIDCompletesOnlyMatch(t *testing.T) {
	s := newTestStore(time.Hour)

	s.HandleHookEvent(models.HookPayload{
		HookEventName: "SubagentStart",
		SessionID:     "s1",
		AgentID:       "agent-a",
		AgentType:     "investigator",
		Description:   "investigate A",
	})
	s.HandleHookEvent(models.HookPayload{
		HookEventName: "SubagentStart",
		SessionID:     "s1",
		AgentID:       "agent-b",
		AgentType:     "investigator",
		Description:   "investigate B",
	})

	s.HandleHookEvent(models.HookPayload{
		HookEventName: "SubagentStop",
		SessionID:     "s1",
		AgentID:       "agent-a",
	})

	sess := mustSession(t, s, "s1")
	if len(sess.ActiveSubagents) != 1 {
		t.Fatalf("active subagents = %d, want 1 (only the matched agent completes): %v",
			len(sess.ActiveSubagents), sess.ActiveSubagents)
	}
	if _, ok := sess.ActiveSubagents["agent-b"]; !ok {
		t.Fatalf("agent-b should still be running, active = %v", sess.ActiveSubagents)
	}
	if len(sess.SubagentHistory) != 1 {
		t.Fatalf("subagent history = %d, want 1", len(sess.SubagentHistory))
	}
	if sess.SubagentHistory[0].ID != "agent-a" {
		t.Fatalf("completed subagent = %q, want %q", sess.SubagentHistory[0].ID, "agent-a")
	}
	if sess.SubagentHistory[0].Status != "completed" {
		t.Fatalf("completed subagent status = %q, want completed", sess.SubagentHistory[0].Status)
	}
}

// --- 5. stalled fallback: one stalled notification per idle episode ----------

func TestStalledFallbackNotifiesOnce(t *testing.T) {
	s := newTestStore(200 * time.Millisecond)

	s.HandleHookEvent(models.HookPayload{
		HookEventName: "UserPromptSubmit",
		SessionID:     "s1",
	})

	// Fresh activity: below the timeout, nothing may fire.
	s.checkLivenessAndAutoIdle()
	if got := notifCount(s); got != 0 {
		t.Fatalf("notifications before timeout = %d, want 0", got)
	}

	backdate(s, "s1", time.Minute)
	s.checkLivenessAndAutoIdle()

	sess := mustSession(t, s, "s1")
	if sess.Status != models.StatusIdle {
		t.Fatalf("status after stall = %q, want %q", sess.Status, models.StatusIdle)
	}
	if got := notifCount(s); got != 1 {
		t.Fatalf("notifications after stall = %d, want 1", got)
	}
	notif := latestNotif(t, s)
	if notif.Type != "stalled" {
		t.Fatalf("stall notification type = %q, want %q", notif.Type, "stalled")
	}
	if notif.Title != "⚠️ No Events for 1m" {
		t.Fatalf("stall notification title = %q, want %q", notif.Title, "⚠️ No Events for 1m")
	}
	if got := countNotifType(s, "task_done"); got != 0 {
		t.Fatalf("task_done after stall = %d, want 0 (a stall is never a completion)", got)
	}

	// Second tick: anti-flap — no second stalled notification.
	backdate(s, "s1", 2*time.Minute)
	forceStatus(s, "s1", models.StatusActive)
	s.checkLivenessAndAutoIdle()
	if got := notifCount(s); got != 1 {
		t.Fatalf("notifications after second tick = %d, want still 1 (anti-flap)", got)
	}
}

// --- 6. UserPromptSubmit: turn start, no notification ------------------------

func TestUserPromptSubmitClearsStateWithoutNotification(t *testing.T) {
	s := NewStore(0, nil) // production constructor must keep working

	s.HandleHookEvent(models.HookPayload{
		HookEventName: "PreToolUse",
		SessionID:     "s1",
		ToolName:      "AskUserQuestion",
		ToolUseID:     "q1",
	})
	if sess := mustSession(t, s, "s1"); sess.PendingQuestion == nil {
		t.Fatal("setup: PendingQuestion should be set")
	}
	before := notifCount(s)

	s.HandleHookEvent(models.HookPayload{
		HookEventName: "UserPromptSubmit",
		SessionID:     "s1",
	})

	sess := mustSession(t, s, "s1")
	if sess.Status != models.StatusActive {
		t.Fatalf("status = %q, want %q", sess.Status, models.StatusActive)
	}
	if sess.PendingQuestion != nil {
		t.Fatalf("PendingQuestion = %+v, want nil after UserPromptSubmit", sess.PendingQuestion)
	}
	if !sess.TurnActive {
		t.Fatal("TurnActive = false, want true after UserPromptSubmit")
	}
	if sess.CurrentToolStatus != "Working on your prompt…" {
		t.Fatalf("tool status = %q, want %q", sess.CurrentToolStatus, "Working on your prompt…")
	}
	if after := notifCount(s); after != before {
		t.Fatalf("notifications = %d before, %d after UserPromptSubmit (turn start must not notify)", before, after)
	}
}

// --- 7. watcher dedup: replays notify nothing, new ids backfill silently -----

func TestWatcherSourceDedupAndSilentBackfill(t *testing.T) {
	s := NewStore(0, nil)

	// The hook (Source "") delivers tool X first.
	s.HandleHookEvent(models.HookPayload{
		HookEventName: "PreToolUse",
		SessionID:     "s1",
		ToolName:      "Task",
		ToolUseID:     "X",
		ToolInput:     map[string]interface{}{"description": "hook-delivered task"},
	})
	if got := notifCount(s); got != 1 {
		t.Fatalf("setup: notifications = %d, want 1 (hook Task notifies)", got)
	}

	// Watcher replays the same tool_use id: no notification, no resurrect.
	s.HandleHookEvent(models.HookPayload{
		HookEventName: "PreToolUse",
		SessionID:     "s1",
		ToolName:      "Task",
		ToolUseID:     "X",
		Source:        "watcher",
		ToolInput:     map[string]interface{}{"description": "replayed task"},
	})
	if got := notifCount(s); got != 1 {
		t.Fatalf("notifications after watcher replay = %d, want still 1", got)
	}
	sess := mustSession(t, s, "s1")
	if len(sess.ActiveSubagents) != 1 {
		t.Fatalf("active subagents after replay = %d, want 1 (no resurrect): %v",
			len(sess.ActiveSubagents), sess.ActiveSubagents)
	}

	// Watcher sees a NEW tool id: state backfill (tool status) but NO notification.
	s.HandleHookEvent(models.HookPayload{
		HookEventName: "PreToolUse",
		SessionID:     "s1",
		ToolName:      "Bash",
		ToolUseID:     "Y",
		Source:        "watcher",
		ToolInput:     map[string]interface{}{"command": "npm install"},
	})
	if got := notifCount(s); got != 1 {
		t.Fatalf("notifications after watcher backfill = %d, want still 1 (watcher never notifies)", got)
	}
	sess = mustSession(t, s, "s1")
	if _, ok := sess.ActiveToolIDs["Y"]; !ok {
		t.Fatalf("watcher backfill did not register tool Y: %v", sess.ActiveToolIDs)
	}
}

// --- 8. Notification handler typing ------------------------------------------

func TestNotificationHandlerTyping(t *testing.T) {
	s := NewStore(0, nil)

	send := func(notifType, message string) {
		t.Helper()
		s.HandleHookEvent(models.HookPayload{
			HookEventName:    "Notification",
			SessionID:        "s1",
			NotificationType: notifType,
			Message:          message,
		})
	}

	send("agent_needs_input", "agent waiting")
	if got := latestNotif(t, s).Type; got != "permission" {
		t.Fatalf("agent_needs_input type = %q, want permission", got)
	}

	send("info", "this response needs your input before continuing")
	if got := latestNotif(t, s).Type; got != "permission" {
		t.Fatalf("message with 'needs your input' type = %q, want permission", got)
	}

	send("info", "A Permission decision is required for Bash")
	if got := latestNotif(t, s).Type; got != "permission" {
		t.Fatalf("message with 'permission' type = %q, want permission", got)
	}

	send("info", "Build finished successfully")
	if got := latestNotif(t, s).Type; got != "info" {
		t.Fatalf("plain message type = %q, want info", got)
	}
}

// --- 9. waiting_permission stale downgrade after 30 quiet minutes -------------

func TestWaitingPermissionStaleDowngrade(t *testing.T) {
	s := newTestStore(200 * time.Millisecond)

	s.HandleHookEvent(models.HookPayload{
		HookEventName: "PreToolUse",
		SessionID:     "s1",
		ToolName:      "AskUserQuestion",
		ToolUseID:     "q1",
	})

	backdate(s, "s1", 31*time.Minute)
	s.checkLivenessAndAutoIdle()

	sess := mustSession(t, s, "s1")
	if sess.Status != models.StatusIdle {
		t.Fatalf("status after 30min wait = %q, want %q", sess.Status, models.StatusIdle)
	}
	if sess.PendingQuestion == nil {
		t.Fatal("PendingQuestion should remain (flagged stale), got nil")
	}
	if !sess.PendingQuestion.Stale {
		t.Fatal("PendingQuestion.Stale = false, want true")
	}
	// Only the original question notification — no stalled, no task_done.
	if got := notifCount(s); got != 1 {
		t.Fatalf("notifications = %d, want 1 (question only)", got)
	}
}

// --- 10. snapshot hygiene: 24h eviction + 20-session cap + deterministic pick -

func TestSnapshotEvictionCapAndDeterministicActiveSession(t *testing.T) {
	s := newTestStore(time.Hour)

	// One session idle for 25h must be evicted entirely.
	s.GetOrCreateSession("ancient", "d:\\proj\\old", "old.jsonl")
	backdate(s, "ancient", 25*time.Hour)

	// 25 fresh but non-working sessions: snapshot keeps the 20 most recent.
	for i := 0; i < 25; i++ {
		s.GetOrCreateSession(fmt.Sprintf("sess-%02d", i), `d:\proj\p`, "t.jsonl")
	}
	base := time.Now().Add(-time.Hour)
	s.mu.Lock()
	for i := 0; i < 25; i++ {
		sess := s.sessions[fmt.Sprintf("sess-%02d", i)]
		sess.Status = models.StatusIdle
		sess.LastActivity = base.Add(time.Duration(i) * time.Minute)
	}
	s.mu.Unlock()

	s.checkLivenessAndAutoIdle()

	snap := s.GetSnapshot()
	if len(snap.Sessions) > 20 {
		t.Fatalf("snapshot sessions = %d, want <= 20", len(snap.Sessions))
	}
	found := map[string]bool{}
	for _, sess := range snap.Sessions {
		found[sess.ID] = true
	}
	if found["ancient"] {
		t.Fatal("25h-old session still present in snapshot, want evicted")
	}
	if !found["sess-24"] {
		t.Fatal("most recent session missing from capped snapshot")
	}
	if found["sess-00"] || found["sess-01"] || found["sess-02"] || found["sess-03"] || found["sess-04"] {
		t.Fatal("oldest sessions beyond the 20 cap should be dropped")
	}

	s.mu.RLock()
	_, stillInMemory := s.sessions["ancient"]
	s.mu.RUnlock()
	if stillInMemory {
		t.Fatal("25h-old session still in memory map, want deleted")
	}

	// No working session exists: ActiveSession must be the most recent
	// activity, deterministically.
	if snap.ActiveSession == nil || snap.ActiveSession.ID != "sess-24" {
		t.Fatalf("ActiveSession = %v, want sess-24 (most recent LastActivity)", snap.ActiveSession)
	}
}
