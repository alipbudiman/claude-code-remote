package watcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claude-remote-server/internal/models"
	"claude-remote-server/internal/state"
)

// --- test helpers -----------------------------------------------------------

// writeTranscript creates projects/<project>/<session>.jsonl with the given
// content and returns its path.
func writeTranscript(t *testing.T, projectsDir, project, session, content string) string {
	t.Helper()
	dir := filepath.Join(projectsDir, project)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	p := filepath.Join(dir, session+".jsonl")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return p
}

func activeTools(t *testing.T, s *state.Store, sessionID string) map[string]string {
	t.Helper()
	for _, sess := range s.GetSnapshot().Sessions {
		if sess.ID == sessionID {
			return sess.ActiveToolIDs
		}
	}
	t.Fatalf("session %q not found in snapshot", sessionID)
	return nil
}

// --- 1. tool_result synthesis retires in-flight tools (no phantoms) -----------

// The tool was registered by the LIVE hook channel; the watcher then sees both
// the tool_use block (deduped by the seen-set) and the transcript tool_result.
// The seen-set must NOT block the synthesized PostToolUse.
func TestToolResultRetiresHookRegisteredTool(t *testing.T) {
	projects := filepath.Join(t.TempDir(), "projects")
	offsets := filepath.Join(t.TempDir(), "offsets.json")
	s := state.NewStore(0, nil)
	tw := NewTranscriptWatcherWithPaths(s, projects, offsets)

	// Hook delivered the PreToolUse (Source "") before the transcript caught up.
	s.HandleHookEvent(models.HookPayload{
		HookEventName: "PreToolUse", SessionID: "sess-1", ToolName: "Bash", ToolUseID: "toolu_01",
	})

	writeTranscript(t, projects, "proj", "sess-1", strings.Join([]string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01","name":"Bash","input":{"command":"go build ./..."}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":"ok"}]}}`,
	}, "\n")+"\n")

	tw.scan()

	if tools := activeTools(t, s, "sess-1"); len(tools) != 0 {
		t.Fatalf("ActiveToolIDs after transcript tool_result = %v, want empty (no phantom in-flight tool)", tools)
	}
}

// Hooks were dead entirely: the watcher alone registered the tool_use and its
// tool_result must still retire it.
func TestToolResultRetiresWatcherRegisteredTool(t *testing.T) {
	projects := filepath.Join(t.TempDir(), "projects")
	offsets := filepath.Join(t.TempDir(), "offsets.json")
	s := state.NewStore(0, nil)
	tw := NewTranscriptWatcherWithPaths(s, projects, offsets)

	writeTranscript(t, projects, "proj", "sess-1", strings.Join([]string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_02","name":"Read","input":{"file_path":"a.go"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_02","content":"file contents"}]}}`,
	}, "\n")+"\n")

	tw.scan()

	if tools := activeTools(t, s, "sess-1"); len(tools) != 0 {
		t.Fatalf("ActiveToolIDs after watcher-only tool lifecycle = %v, want empty", tools)
	}
}

// A user prompt record (content is a plain string or text blocks) must not be
// mistaken for a tool_result.
func TestUserPromptRecordTriggersNothing(t *testing.T) {
	projects := filepath.Join(t.TempDir(), "projects")
	offsets := filepath.Join(t.TempDir(), "offsets.json")
	s := state.NewStore(0, nil)
	tw := NewTranscriptWatcherWithPaths(s, projects, offsets)

	writeTranscript(t, projects, "proj", "sess-1", strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"just a plain prompt"}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"structured prompt"}]}}`,
	}, "\n")+"\n")

	tw.scan()

	if tools := activeTools(t, s, "sess-1"); len(tools) != 0 {
		t.Fatalf("ActiveToolIDs after plain user records = %v, want empty", tools)
	}
	if got := len(s.GetSnapshot().Notifications); got != 0 {
		t.Fatalf("notifications = %d, want 0 (user records never notify)", got)
	}
}

// --- 1b. M9: transcript-based turn-end detection -------------------------------

// sessionState fetches a session for assertions (nil if absent).
func sessionState(t *testing.T, s *state.Store, sessionID string) *models.Session {
	t.Helper()
	for _, sess := range s.GetSnapshot().Sessions {
		if sess.ID == sessionID {
			return sess
		}
	}
	return nil
}

// A transcript turn that ends with a final text answer (stop_reason
// "end_turn": thinking block first, then the text block) must synthesize a
// watcher-source Stop: the session goes idle with the explicit completed
// display and exactly one task_done notification — even though no hook ever
// fired (the watcher-tracked subagent case).
func TestFinalTextRecordSynthesizesStop(t *testing.T) {
	projects := filepath.Join(t.TempDir(), "projects")
	offsets := filepath.Join(t.TempDir(), "offsets.json")
	s := state.NewStore(0, nil)
	tw := NewTranscriptWatcherWithPaths(s, projects, offsets)

	writeTranscript(t, projects, "proj", "sess-1", strings.Join([]string{
		`{"type":"assistant","message":{"id":"msg_a","role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_01","name":"Bash","input":{"command":"go build ./..."}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":"ok"}]}}`,
		`{"type":"assistant","message":{"id":"msg_b","role":"assistant","stop_reason":"end_turn","content":[{"type":"thinking","thinking":"wrapping up…"}]}}`,
		`{"type":"assistant","message":{"id":"msg_b","role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"Done. The build passes."}]}}`,
	}, "\n")+"\n")

	tw.scan()

	sess := sessionState(t, s, "sess-1")
	if sess == nil {
		t.Fatal("session sess-1 not created by watcher scan")
	}
	if sess.Status != models.StatusIdle {
		t.Fatalf("status after final text record = %q, want %q (synthesized Stop)", sess.Status, models.StatusIdle)
	}
	if sess.CurrentToolStatus != "✅ Task completed" {
		t.Fatalf("tool status after final text record = %q, want %q", sess.CurrentToolStatus, "✅ Task completed")
	}
	if sess.LastCompletedAt == nil {
		t.Fatal("LastCompletedAt = nil after synthesized Stop, want timestamp")
	}
	taskDone := 0
	for _, notif := range s.GetSnapshot().Notifications {
		if notif.Type == "task_done" {
			taskDone++
		}
	}
	if taskDone != 1 {
		t.Fatalf("task_done notifications = %d, want exactly 1 (got all: %+v)", taskDone, s.GetSnapshot().Notifications)
	}
}

// Mid-turn records must NOT synthesize a Stop: a narration text record that is
// part of a message which later streams tool_use blocks (stop_reason
// "tool_use"), a record containing a tool_use block, and a thinking-only
// end_turn record (the final answer's reasoning chunk — the text block has not
// arrived yet) are all silent.
func TestMidTurnRecordsDoNotSynthesizeStop(t *testing.T) {
	projects := filepath.Join(t.TempDir(), "projects")
	offsets := filepath.Join(t.TempDir(), "offsets.json")
	s := state.NewStore(0, nil)
	tw := NewTranscriptWatcherWithPaths(s, projects, offsets)

	writeTranscript(t, projects, "proj", "sess-1", strings.Join([]string{
		// Mid-turn narration: text block, but stop_reason says tool_use follows.
		`{"type":"assistant","message":{"id":"msg_a","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"Let me check the build first."}]}}`,
		// A tool_use record in the same message.
		`{"type":"assistant","message":{"id":"msg_a","role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_01","name":"Bash","input":{"command":"go test ./..."}}]}}`,
		// Thinking-only end_turn chunk of a final answer whose text block
		// never arrived in this transcript (interrupted session).
		`{"type":"assistant","message":{"id":"msg_c","role":"assistant","stop_reason":"end_turn","content":[{"type":"thinking","thinking":"so the answer is…"}]}}`,
	}, "\n")+"\n")

	tw.scan()

	sess := sessionState(t, s, "sess-1")
	if sess == nil {
		t.Fatal("session sess-1 not created by watcher scan")
	}
	if sess.Status != models.StatusActive {
		t.Fatalf("status after mid-turn records = %q, want %q (no Stop may be synthesized)", sess.Status, models.StatusActive)
	}
	if sess.LastCompletedAt != nil {
		t.Fatalf("LastCompletedAt = %v after mid-turn records, want nil", sess.LastCompletedAt)
	}
	if got := len(s.GetSnapshot().Notifications); got != 0 {
		t.Fatalf("notifications = %d, want 0 (no completion from mid-turn records): %+v", got, s.GetSnapshot().Notifications)
	}
}

// --- 2. offsets round-trip ----------------------------------------------------

func TestOffsetsRoundTrip(t *testing.T) {
	projects := filepath.Join(t.TempDir(), "projects")
	offsets := filepath.Join(t.TempDir(), "offsets.json")

	tw := NewTranscriptWatcherWithPaths(state.NewStore(0, nil), projects, offsets)
	tw.fileOffsets["a.jsonl"] = 123
	tw.fileOffsets["b.jsonl"] = 456789
	tw.saveOffsets()

	tw2 := NewTranscriptWatcherWithPaths(state.NewStore(0, nil), projects, offsets)
	tw2.loadOffsets()
	if len(tw2.fileOffsets) != 2 || tw2.fileOffsets["a.jsonl"] != 123 || tw2.fileOffsets["b.jsonl"] != 456789 {
		t.Fatalf("offsets round-trip = %v, want {a.jsonl:123, b.jsonl:456789}", tw2.fileOffsets)
	}
}

// A corrupt offsets file must not brick the watcher: it starts fresh.
func TestCorruptOffsetsStartFresh(t *testing.T) {
	projects := filepath.Join(t.TempDir(), "projects")
	offsets := filepath.Join(t.TempDir(), "offsets.json")
	if err := os.WriteFile(offsets, []byte("not json at all"), 0644); err != nil {
		t.Fatal(err)
	}

	tw := NewTranscriptWatcherWithPaths(state.NewStore(0, nil), projects, offsets)
	tw.loadOffsets()
	if len(tw.fileOffsets) != 0 {
		t.Fatalf("offsets after corrupt load = %v, want empty", tw.fileOffsets)
	}
}

// Stop() runs on the shutdown path while a scan may be mid-pass: it must wait
// out the in-flight scan, persist offsets, and never deadlock or panic. The
// deferred/double Stop must stay a no-op.
func TestStopDuringScanWaitsAndSavesOffsets(t *testing.T) {
	projects := filepath.Join(t.TempDir(), "projects")
	offsets := filepath.Join(t.TempDir(), "offsets.json")
	tw := NewTranscriptWatcherWithPaths(state.NewStore(0, nil), projects, offsets)
	tw.fileOffsets["live.jsonl"] = 777

	done := make(chan struct{})
	go func() {
		defer close(done)
		tw.scan() // concurrent full pass (projects dir absent -> cheap early return)
	}()
	tw.Stop() // must not deadlock against the in-flight scan
	<-done

	// Offsets were persisted despite the concurrent scan.
	data, err := os.ReadFile(offsets)
	if err != nil {
		t.Fatalf("offsets file missing after Stop: %v", err)
	}
	if !strings.Contains(string(data), `"live.jsonl":777`) {
		t.Fatalf("offsets content = %s, want live.jsonl:777 persisted", string(data))
	}

	// Double Stop (the deferred call on the shutdown path) is a no-op.
	tw.Stop()
}

// --- live process feed extraction (2026-09-02) -------------------------------

func TestHandleAssistantRecordExtractsThinkingAndText(t *testing.T) {
	store := state.NewStore(0, nil)
	tw := NewTranscriptWatcherWithPaths(store, t.TempDir(), filepath.Join(t.TempDir(), "offsets.json"))
	sess := store.GetOrCreateSession("s1", "proj", "p.jsonl")
	rec := map[string]interface{}{
		"type": "assistant",
		"message": map[string]interface{}{
			"id": "msg_1", "stop_reason": "tool_use",
			"content": []interface{}{
				map[string]interface{}{"type": "thinking", "thinking": "let me plan"},
				map[string]interface{}{"type": "text", "text": "Working on it"},
				map[string]interface{}{"type": "tool_use", "id": "t1", "name": "Bash",
					"input": map[string]interface{}{"command": "ls"}},
			},
		},
	}
	tw.handleAssistantRecord(sess, rec)
	evts := store.ProcessEvents("s1", 0, 50)
	kinds := map[string]bool{}
	for _, e := range evts {
		kinds[string(e.Kind)] = true
	}
	if !kinds["thinking"] || !kinds["text"] || !kinds["tool_use"] {
		t.Fatalf("expected thinking+text+tool_use events, got %+v", evts)
	}
	for _, e := range evts {
		if e.Kind == "thinking" && e.Detail != "let me plan" {
			t.Fatalf("thinking detail = %q", e.Detail)
		}
	}
}

func TestHandleAssistantRecordFinalTextNotDuplicated(t *testing.T) {
	store := state.NewStore(0, nil)
	tw := NewTranscriptWatcherWithPaths(store, t.TempDir(), filepath.Join(t.TempDir(), "offsets.json"))
	sess := store.GetOrCreateSession("s1", "proj", "p.jsonl")
	// A final answer record (stop_reason=end_turn) must not emit a text
	// event — the turn_end Stop event carries last_assistant_message.
	rec := map[string]interface{}{
		"type": "assistant",
		"message": map[string]interface{}{
			"id": "msg_2", "stop_reason": "end_turn",
			"content": []interface{}{map[string]interface{}{"type": "text", "text": "Done!"}},
		},
	}
	tw.handleAssistantRecord(sess, rec)
	for _, e := range store.ProcessEvents("s1", 0, 50) {
		if e.Kind == models.EventText {
			t.Fatalf("final-answer text must not emit a text event, got %+v", e)
		}
	}
}

func TestHandleUserRecordExtractsToolResultContent(t *testing.T) {
	store := state.NewStore(0, nil)
	tw := NewTranscriptWatcherWithPaths(store, t.TempDir(), filepath.Join(t.TempDir(), "offsets.json"))
	sess := store.GetOrCreateSession("s1", "proj", "p.jsonl")
	rec := map[string]interface{}{
		"type": "user",
		"message": map[string]interface{}{"role": "user", "content": []interface{}{
			map[string]interface{}{"type": "tool_result", "tool_use_id": "t1",
				"content": []interface{}{map[string]interface{}{"type": "text", "text": "file-a\nfile-b"}}},
		}},
	}
	tw.handleUserRecord(sess, rec)
	found := false
	for _, e := range store.ProcessEvents("s1", 0, 50) {
		if e.Kind == models.EventToolResult && e.ToolUseID == "t1" && strings.Contains(e.Detail, "file-a") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected tool_result event with content, got %+v", store.ProcessEvents("s1", 0, 50))
	}
}
