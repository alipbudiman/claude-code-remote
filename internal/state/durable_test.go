package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-remote-server/internal/models"
)

// --- test helpers -----------------------------------------------------------

// readLogLines returns the non-empty lines of a file.
func readLogLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var lines []string
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// writeSpool writes raw lines to the spool file inside dir.
func writeSpool(t *testing.T, dir string, lines []string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, SpoolName), []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatalf("write spool: %v", err)
	}
}

// appendLogEntry appends one raw entry to the event log with a controlled
// timestamp (bypasses HandleHookEvent so the window filter is testable).
func appendLogEntry(t *testing.T, dir string, ts time.Time, payload models.HookPayload) {
	t.Helper()
	line, err := json.Marshal(eventLogEntry{TS: ts, Event: payload})
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, eventLogName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		t.Fatalf("append entry: %v", err)
	}
}

// --- 1. event log: one line per ACCEPTED event --------------------------------

func TestEventLogAppendsOneLinePerAcceptedEvent(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(0, nil)
	s.SetEventLog(NewEventLog(dir))

	s.HandleHookEvent(models.HookPayload{HookEventName: "UserPromptSubmit", SessionID: "s1"})
	s.HandleHookEvent(models.HookPayload{HookEventName: "PreToolUse", SessionID: "s1", ToolName: "Bash", ToolUseID: "t1"})
	s.HandleHookEvent(models.HookPayload{HookEventName: "Stop", SessionID: "s1"})

	// A watcher replay duplicate is NOT an accepted event — must not be logged.
	s.HandleHookEvent(models.HookPayload{
		HookEventName: "PreToolUse", SessionID: "s1", ToolName: "Bash", ToolUseID: "t1", Source: "watcher",
	})

	lines := readLogLines(t, filepath.Join(dir, eventLogName))
	if len(lines) != 3 {
		t.Fatalf("event log lines = %d, want 3 (watcher duplicate must not be logged): %q", len(lines), lines)
	}
	var first eventLogEntry
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("line 0 is not an event log entry: %v", err)
	}
	if first.Event.HookEventName != "UserPromptSubmit" {
		t.Fatalf("first logged event = %q, want UserPromptSubmit (apply order preserved)", first.Event.HookEventName)
	}
}

// --- 2. boot replay rebuilds state, produces ZERO notifications ----------------

func TestBootReplayRebuildsStateAndProducesZeroNotifications(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(0, nil)
	s.SetEventLog(NewEventLog(dir))

	// s1: a pending question (normally notification-worthy).
	s.HandleHookEvent(models.HookPayload{HookEventName: "UserPromptSubmit", SessionID: "s1"})
	s.HandleHookEvent(models.HookPayload{HookEventName: "PreToolUse", SessionID: "s1", ToolName: "AskUserQuestion", ToolUseID: "q1"})
	// s2: left mid-run with a tool in flight.
	s.HandleHookEvent(models.HookPayload{HookEventName: "UserPromptSubmit", SessionID: "s2"})
	s.HandleHookEvent(models.HookPayload{HookEventName: "PreToolUse", SessionID: "s2", ToolName: "Bash", ToolUseID: "t2"})

	replayed := NewStore(0, nil)
	n, err := NewEventLog(dir).Replay(replayed.HandleHookEvent)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if n != 4 {
		t.Fatalf("replayed %d events, want 4", n)
	}

	if got := notifCount(replayed); got != 0 {
		t.Fatalf("replay notifications = %d, want 0 (replayed history must not re-buzz): %+v",
			got, replayed.GetSnapshot().Notifications)
	}

	sess1 := mustSession(t, replayed, "s1")
	if sess1.Status != models.StatusWaitingPermission {
		t.Fatalf("s1 status after replay = %q, want %q", sess1.Status, models.StatusWaitingPermission)
	}
	if sess1.PendingQuestion == nil || sess1.PendingQuestion.ToolUseID != "q1" {
		t.Fatalf("s1 PendingQuestion after replay = %+v, want question q1", sess1.PendingQuestion)
	}

	sess2 := mustSession(t, replayed, "s2")
	if _, ok := sess2.ActiveToolIDs["t2"]; !ok {
		t.Fatalf("s2 tool t2 not in flight after replay: %v", sess2.ActiveToolIDs)
	}
	if sess2.Status != models.StatusActive {
		t.Fatalf("s2 status after replay = %q, want %q", sess2.Status, models.StatusActive)
	}
	if !sess2.TurnActive {
		t.Fatal("s2 TurnActive = false after replay, want true")
	}
}

// --- 3. replay bounds: 24h window, malformed skip, 10k line cap ----------------

func TestReplayWindowAndMalformedSkip(t *testing.T) {
	dir := t.TempDir()

	appendLogEntry(t, dir, time.Now().Add(-25*time.Hour),
		models.HookPayload{HookEventName: "Stop", SessionID: "old"}) // outside 24h window
	appendLogEntry(t, dir, time.Now().Add(-time.Hour),
		models.HookPayload{HookEventName: "UserPromptSubmit", SessionID: "recent"})

	// A malformed line between valid entries must be skipped, not fatal.
	f, err := os.OpenFile(filepath.Join(dir, eventLogName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{not json}\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	var fed []models.HookPayload
	n, err := NewEventLog(dir).Replay(func(p models.HookPayload) { fed = append(fed, p) })
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if n != 1 || len(fed) != 1 {
		t.Fatalf("replayed %d events (fed %d), want 1 (old entry + malformed line skipped)", n, len(fed))
	}
	if fed[0].SessionID != "recent" || fed[0].Source != "replay" {
		t.Fatalf("fed event = %+v, want recent/UserPromptSubmit with Source=replay", fed[0])
	}
}

func TestReplayCapsAtMostTenThousandLines(t *testing.T) {
	dir := t.TempDir()
	total := replayMaxLines + 500
	for i := 0; i < total; i++ {
		appendLogEntry(t, dir, time.Now(), models.HookPayload{
			HookEventName: "UserPromptSubmit", SessionID: "s",
		})
	}

	var fed []models.HookPayload
	n, err := NewEventLog(dir).Replay(func(p models.HookPayload) { fed = append(fed, p) })
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if n != replayMaxLines {
		t.Fatalf("replayed %d events, want capped at %d", n, replayMaxLines)
	}
}

// --- 4. spool drain: ordered, notifies, skips malformed, truncates -------------

func TestSpoolDrainProcessesInOrderNotifiesAndTruncates(t *testing.T) {
	dir := t.TempDir()
	writeSpool(t, dir, []string{
		`{"hook_event_name":"PermissionRequest","session_id":"s1","tool_name":"Bash","tool_use_id":"p1"}`,
		`{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"AskUserQuestion","tool_use_id":"q1"}`,
		`<<<malformed line>>>`,
		`{"hook_event_name":"Stop","session_id":"s1"}`,
	})

	s := NewStore(0, nil)
	n, err := DrainSpool(dir, s.HandleHookEvent)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if n != 3 {
		t.Fatalf("drained %d events, want 3 (malformed line skipped)", n)
	}

	// Spooled events are REAL events the user never saw: all three must notify.
	if got := notifCount(s); got != 3 {
		t.Fatalf("notifications after drain = %d, want 3: %+v", got, s.GetSnapshot().Notifications)
	}
	// Notification list is newest-first: file order was permission, question, Stop.
	notifs := s.GetSnapshot().Notifications
	if notifs[0].Type != "task_done" {
		t.Fatalf("newest notification type = %q, want task_done (Stop was last in file)", notifs[0].Type)
	}
	if notifs[1].Type != "permission" || notifs[2].Type != "permission" {
		t.Fatalf("older notifications = %q/%q, want permission/permission (file order)",
			notifs[1].Type, notifs[2].Type)
	}

	// Session ends idle (all three events applied in order).
	sess := mustSession(t, s, "s1")
	if sess.Status != models.StatusIdle {
		t.Fatalf("session status after drain = %q, want idle (Stop applied last)", sess.Status)
	}

	// File must be truncated to empty.
	st, err := os.Stat(filepath.Join(dir, SpoolName))
	if err != nil {
		t.Fatalf("stat spool: %v", err)
	}
	if st.Size() != 0 {
		t.Fatalf("spool size after drain = %d, want 0", st.Size())
	}

	// Second drain: no-op, no duplicate notifications.
	n2, err := DrainSpool(dir, s.HandleHookEvent)
	if err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("second drain processed %d, want 0", n2)
	}
	if got := notifCount(s); got != 3 {
		t.Fatalf("notifications after second drain = %d, want still 3", got)
	}
}

func TestSpoolDrainNoOpWhenFileMissing(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(0, nil)
	n, err := DrainSpool(dir, s.HandleHookEvent)
	if err != nil || n != 0 {
		t.Fatalf("drain on missing file = (%d, %v), want (0, nil)", n, err)
	}
}

// --- 5. drained spool events also land in the event log (next boot sees them) --

func TestSpoolDrainFeedsEventLog(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(0, nil)
	s.SetEventLog(NewEventLog(dir))
	writeSpool(t, dir, []string{
		`{"hook_event_name":"UserPromptSubmit","session_id":"sx"}`,
	})

	if _, err := DrainSpool(dir, s.HandleHookEvent); err != nil {
		t.Fatalf("drain: %v", err)
	}

	if lines := readLogLines(t, filepath.Join(dir, eventLogName)); len(lines) != 1 {
		t.Fatalf("event log lines after drain = %d, want 1 (drained events persist)", len(lines))
	}

	// A fresh boot must replay the drained event.
	fresh := NewStore(0, nil)
	n, err := NewEventLog(dir).Replay(fresh.HandleHookEvent)
	if err != nil || n != 1 {
		t.Fatalf("replay after drain = (%d, %v), want (1, nil)", n, err)
	}
	mustSession(t, fresh, "sx")
}

// --- 6. restart continuity: in-flight tool survives, then retires --------------

func TestRestartContinuityInFlightToolSurvives(t *testing.T) {
	dir := t.TempDir()

	// Store A (pre-restart): tool t1 starts, prompt submitted.
	a := NewStore(0, nil)
	a.SetEventLog(NewEventLog(dir))
	a.HandleHookEvent(models.HookPayload{HookEventName: "PreToolUse", SessionID: "s1", ToolName: "Bash", ToolUseID: "t1"})
	a.HandleHookEvent(models.HookPayload{HookEventName: "UserPromptSubmit", SessionID: "s1"})

	// Store B (post-restart): replays the log from the same directory.
	b := NewStore(0, nil)
	if _, err := NewEventLog(dir).Replay(b.HandleHookEvent); err != nil {
		t.Fatalf("replay: %v", err)
	}
	sess := mustSession(t, b, "s1")
	if _, ok := sess.ActiveToolIDs["t1"]; !ok {
		t.Fatalf("tool t1 not in flight after restart: %v", sess.ActiveToolIDs)
	}

	// Live completion after the restart retires it.
	b.HandleHookEvent(models.HookPayload{HookEventName: "PostToolUse", SessionID: "s1", ToolUseID: "t1"})
	sess = mustSession(t, b, "s1")
	if _, ok := sess.ActiveToolIDs["t1"]; ok {
		t.Fatalf("tool t1 still in flight after PostToolUse: %v", sess.ActiveToolIDs)
	}
	if sess.Status != models.StatusActive {
		t.Fatalf("status after PostToolUse = %q, want %q", sess.Status, models.StatusActive)
	}
	if sess.CurrentToolStatus != "Processing results" {
		t.Fatalf("tool status after PostToolUse = %q, want %q", sess.CurrentToolStatus, "Processing results")
	}
}

// --- 7. replay must NOT re-append to the event log (production wiring) ---------

// The production boot path attaches the event log BEFORE replaying. Replay is
// a re-feed of lines already in the log: if those events were appended again
// (with fresh timestamps), every restart would duplicate up to 10k lines, keep
// ancient events "recent" forever, and inflate the Replayed-N count.
func TestReplayDoesNotReAppendToEventLog(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, eventLogName)

	// Pre-restart store: live events are logged.
	a := NewStore(0, nil)
	a.SetEventLog(NewEventLog(dir))
	a.HandleHookEvent(models.HookPayload{HookEventName: "PreToolUse", SessionID: "s1", ToolName: "Bash", ToolUseID: "t1"})
	a.HandleHookEvent(models.HookPayload{HookEventName: "UserPromptSubmit", SessionID: "s1"})
	before := len(readLogLines(t, logPath))
	if before != 2 {
		t.Fatalf("setup: event log lines = %d, want 2", before)
	}

	// Post-restart store with the log ATTACHED (exactly main.go's wiring),
	// then replay.
	b := NewStore(0, nil)
	b.SetEventLog(NewEventLog(dir))
	n, err := NewEventLog(dir).Replay(b.HandleHookEvent)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if n != 2 {
		t.Fatalf("replayed %d events, want 2", n)
	}
	if after := len(readLogLines(t, logPath)); after != before {
		t.Fatalf("event log lines after replay = %d, want UNCHANGED (%d) — replay must not re-append", after, before)
	}

	// A subsequent LIVE event must still append.
	b.HandleHookEvent(models.HookPayload{HookEventName: "PostToolUse", SessionID: "s1", ToolUseID: "t1"})
	if after := len(readLogLines(t, logPath)); after != before+1 {
		t.Fatalf("event log lines after live event = %d, want %d (live events still append)", after, before+1)
	}
}
