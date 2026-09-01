package watcher

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"claude-remote-server/internal/models"
	"claude-remote-server/internal/state"
)

const (
	// scanInterval is how often transcript directories are polled.
	scanInterval = 2 * time.Second

	// offsetsSaveInterval is how often read offsets are persisted to disk so
	// a restart does not re-tail already-processed transcript history.
	offsetsSaveInterval = 30 * time.Second
)

// TranscriptWatcher monitors ~/.claude/projects for session activity
type TranscriptWatcher struct {
	store       *state.Store
	projectsDir string
	offsetsPath string
	fileOffsets map[string]int64
	mu          sync.Mutex
	// scanMu serializes full scan passes and lets Stop() wait out an
	// in-flight scan so the final offset save includes its progress.
	scanMu   sync.Mutex
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewTranscriptWatcher initializes a new file watcher with production paths
// (~/.claude/projects and ~/.claude-remote-offsets.json)
func NewTranscriptWatcher(store *state.Store) *TranscriptWatcher {
	home, err := os.UserHomeDir()
	projectsDir, offsetsPath := "", ""
	if err == nil {
		projectsDir = filepath.Join(home, ".claude", "projects")
		offsetsPath = filepath.Join(home, ".claude-remote-offsets.json")
	}
	return newTranscriptWatcher(store, projectsDir, offsetsPath)
}

// NewTranscriptWatcherWithPaths is NewTranscriptWatcher with explicit paths so
// tests can point both at temp directories instead of the real home.
func NewTranscriptWatcherWithPaths(store *state.Store, projectsDir, offsetsPath string) *TranscriptWatcher {
	return newTranscriptWatcher(store, projectsDir, offsetsPath)
}

func newTranscriptWatcher(store *state.Store, projectsDir, offsetsPath string) *TranscriptWatcher {
	return &TranscriptWatcher{
		store:       store,
		projectsDir: projectsDir,
		offsetsPath: offsetsPath,
		fileOffsets: make(map[string]int64),
		stopCh:      make(chan struct{}),
	}
}

// Start begins periodic polling of JSONL transcript files
func (tw *TranscriptWatcher) Start() {
	if tw.projectsDir == "" {
		return
	}

	tw.loadOffsets()

	scanTick := time.NewTicker(scanInterval)
	saveTick := time.NewTicker(offsetsSaveInterval)
	go func() {
		defer scanTick.Stop()
		defer saveTick.Stop()
		for {
			select {
			case <-scanTick.C:
				tw.scan()
			case <-saveTick.C:
				tw.saveOffsets()
			case <-tw.stopCh:
				return
			}
		}
	}()
}

// Stop terminates the file watcher, persisting offsets first. It waits for an
// in-flight scan to finish (bounded by one incremental scan pass) so the saved
// offsets include that scan's progress. Safe to call more than once.
func (tw *TranscriptWatcher) Stop() {
	tw.stopOnce.Do(func() {
		close(tw.stopCh)
		tw.scanMu.Lock()
		defer tw.scanMu.Unlock()
		tw.saveOffsets()
	})
}

// loadOffsets restores transcript read positions saved by a previous run. A
// missing file is the normal first-run case; a corrupt file starts fresh
// rather than failing the watcher.
func (tw *TranscriptWatcher) loadOffsets() {
	if tw.offsetsPath == "" {
		return
	}
	data, err := os.ReadFile(tw.offsetsPath)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Printf("⚠️  Transcript watcher: cannot read %s: %v (starting fresh)\n", tw.offsetsPath, err)
		}
		return
	}
	var offsets map[string]int64
	if err := json.Unmarshal(data, &offsets); err != nil {
		fmt.Printf("⚠️  Transcript watcher: offsets file corrupt (%v) — starting fresh\n", err)
		return
	}
	if offsets == nil {
		return
	}
	tw.mu.Lock()
	tw.fileOffsets = offsets
	tw.mu.Unlock()
}

// saveOffsets atomically persists the read positions (temp file + rename so a
// crash mid-write can never leave a truncated offsets file behind).
func (tw *TranscriptWatcher) saveOffsets() {
	if tw.offsetsPath == "" {
		return
	}
	tw.mu.Lock()
	snapshot := make(map[string]int64, len(tw.fileOffsets))
	for k, v := range tw.fileOffsets {
		snapshot[k] = v
	}
	tw.mu.Unlock()

	data, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	tmp := tw.offsetsPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return
	}
	_ = os.Rename(tmp, tw.offsetsPath)
}

func (tw *TranscriptWatcher) scan() {
	tw.scanMu.Lock()
	defer tw.scanMu.Unlock()

	if _, err := os.Stat(tw.projectsDir); os.IsNotExist(err) {
		return
	}

	entries, err := os.ReadDir(tw.projectsDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			projectDir := filepath.Join(tw.projectsDir, entry.Name())
			tw.scanProjectDir(projectDir)
		}
	}
}

func (tw *TranscriptWatcher) scanProjectDir(projectDir string) {
	files, err := os.ReadDir(projectDir)
	if err != nil {
		return
	}

	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".jsonl") {
			filePath := filepath.Join(projectDir, file.Name())
			sessionID := strings.TrimSuffix(file.Name(), ".jsonl")
			tw.processTranscriptFile(sessionID, projectDir, filePath)
		}
	}
}

func (tw *TranscriptWatcher) processTranscriptFile(sessionID, projectDir, filePath string) {
	tw.mu.Lock()
	offset, seenBefore := tw.fileOffsets[filePath]
	tw.mu.Unlock()

	f, err := os.Open(filePath)
	if err != nil {
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return
	}

	// Truncated/rotated transcript (file shrank below our last read
	// position): restart from the beginning so new content is not missed.
	if stat.Size() < offset {
		offset = 0
	}

	// First sighting of a transcript not written to within the last 10
	// minutes: record the current end offset and skip, so a boot-time scan
	// never resurrects historical sessions as phantoms.
	if !seenBefore && time.Since(stat.ModTime()) > 10*time.Minute {
		tw.mu.Lock()
		tw.fileOffsets[filePath] = stat.Size()
		tw.mu.Unlock()
		return
	}

	// First time reading a large live file: start near the end.
	if !seenBefore && offset == 0 && stat.Size() > 50000 {
		offset = stat.Size() - 25000
	}

	if stat.Size() <= offset {
		return
	}

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	var lastProcessedOffset int64 = offset

	sess := tw.store.GetOrCreateSession(sessionID, projectDir, filePath)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var record map[string]interface{}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}

		tw.handleTranscriptRecord(sess, record)
	}

	currPos, err := f.Seek(0, io.SeekCurrent)
	if err == nil {
		lastProcessedOffset = currPos
	}

	tw.mu.Lock()
	tw.fileOffsets[filePath] = lastProcessedOffset
	tw.mu.Unlock()
}

// handleUserRecord processes user-role transcript records. Its job is
// tool_result synthesis: a completed tool_use shows up in the transcript as a
// user record carrying a tool_result block, even when the hook bridge never
// delivered a PostToolUse (server down, hook failure). Synthesizing one
// retires the id from ActiveToolIDs so no phantom in-flight tool can block
// the liveness stall fallback forever.
//
// Observed shape (2026-08-31, real transcripts under
// ~/.claude/projects/d--CODING-claude-status-apk/*.jsonl, 101 records sampled):
//
//	{"type":"user","message":{"role":"user","content":[
//	   {"type":"tool_result","tool_use_id":"call_x…","content":"…"}
//	 ]}, …}
//
// — the record's "type" is "user"; blocks live in message.content[]; the
// tool_use_id sits directly on the block; an optional "is_error" flag marks
// failures; exactly one tool_result block per record in practice (all blocks
// are still iterated). Plain user prompts carry content as a string or text
// blocks and never match.
func (tw *TranscriptWatcher) handleUserRecord(sess *models.Session, record map[string]interface{}) {
	msg, ok := record["message"].(map[string]interface{})
	if !ok {
		return
	}
	blocks, ok := msg["content"].([]interface{})
	if !ok {
		return
	}
	for _, b := range blocks {
		block, ok := b.(map[string]interface{})
		if !ok {
			continue
		}
		if bType, _ := block["type"].(string); bType != "tool_result" {
			continue
		}
		toolID, _ := block["tool_use_id"].(string)
		if toolID == "" {
			continue
		}
		// Source="watcher": redundancy channel — state transitions apply
		// (retire the tool id) but notifications stay suppressed. The
		// seen-tool-id dedup in the store only gates PreToolUse, so this
		// PostToolUse is never blocked by an already-seen id.
		tw.store.HandleHookEvent(models.HookPayload{
			HookEventName: "PostToolUse",
			SessionID:     sess.ID,
			ToolUseID:     toolID,
			Cwd:           sess.ProjectDir,
			Source:        "watcher",
		})
	}
}

// handleAssistantRecord processes assistant-role transcript records. Two
// jobs, both watcher-synthesized (Source:"watcher"):
//
//  1. tool_use synthesis: register each tool_use block as a PreToolUse so a
//     session the hook bridge never tracked still shows live tool activity.
//
//  2. turn-end detection (M9): when the record IS Claude's final answer for
//     the turn, feed the store a Stop. Watcher-tracked sessions (subagents,
//     sessions whose hook delivery failed) never receive the real Stop hook;
//     without this they hang on their "last tool" until the stall fallback.
//
// Observed shape (2026-09-01, real transcripts under ~/.claude/projects/,
// 3 projects / ~1350 assistant records sampled):
//
//	{"type":"assistant","message":{"id":"msg_…","role":"assistant",
//	  "stop_reason":"end_turn","content":[{"type":"text","text":"…"}]}, …}
//
// Content blocks stream as SEPARATE records sharing one message.id
// (thinking → text → tool_use…), and every record of a message carries that
// message's stop_reason. The naive "record is all text blocks" rule does NOT
// identify a final answer: mid-turn narration text records — same message
// that later streams tool_use blocks — are extremely common (288 samples vs
// 31 true finals) but carry stop_reason:"tool_use". Only the turn's final
// answer message carries stop_reason:"end_turn" (each such message also
// streams a thinking-only record first; thinking alone is NEVER a turn-end).
// stop_reason:"stop_sequence" also occurs and is NOT treated as a turn-end.
// So the rule "≥1 text block, 0 tool_use blocks, stop_reason=end_turn" fires
// exactly once per completed turn and never mid-turn.
func (tw *TranscriptWatcher) handleAssistantRecord(sess *models.Session, record map[string]interface{}) {
	var blocks []interface{}
	var stopReason string
	if msg, ok := record["message"].(map[string]interface{}); ok {
		if content, ok := msg["content"].([]interface{}); ok {
			blocks = content
		}
		stopReason, _ = msg["stop_reason"].(string)
	} else if content, ok := record["content"].([]interface{}); ok {
		blocks = content
	}

	hasText, hasToolUse := false, false
	for _, b := range blocks {
		block, ok := b.(map[string]interface{})
		if !ok {
			continue
		}
		switch bType, _ := block["type"].(string); bType {
		case "tool_use":
			hasToolUse = true
			toolName, _ := block["name"].(string)
			toolID, _ := block["id"].(string)
			input, _ := block["input"].(map[string]interface{})

			// Watcher-synthesized events are a redundancy channel:
			// Source="watcher" lets the store dedup replays of ids
			// the hooks already delivered and suppress notifications.
			tw.store.HandleHookEvent(models.HookPayload{
				HookEventName: "PreToolUse",
				SessionID:     sess.ID,
				ToolName:      toolName,
				ToolUseID:     toolID,
				ToolInput:     input,
				Cwd:           sess.ProjectDir,
				Source:        "watcher",
			})
		case "text":
			hasText = true
		}
	}

	// Turn-end: the final answer's text record — a text block, no tool call
	// in the same record, and the message ended on its own ("end_turn").
	// Source="watcher" marks the synthesized Stop; the store's duplicate-Stop
	// guard keeps the completion and its task_done notification exactly-once
	// per turn no matter which source arrives first.
	if hasText && !hasToolUse && stopReason == "end_turn" {
		tw.store.HandleHookEvent(models.HookPayload{
			HookEventName: "Stop",
			SessionID:     sess.ID,
			Cwd:           sess.ProjectDir,
			Source:        "watcher",
		})
	}
}

func (tw *TranscriptWatcher) handleTranscriptRecord(sess *models.Session, record map[string]interface{}) {
	recordType, _ := record["type"].(string)

	if recordType == "user" {
		tw.handleUserRecord(sess, record)
		return
	}

	if recordType == "assistant" {
		tw.handleAssistantRecord(sess, record)
	}
}
