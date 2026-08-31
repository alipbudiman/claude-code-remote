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
		t.Fatalf("notifications = %d, want 0 (watcher never notifies)", got)
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
