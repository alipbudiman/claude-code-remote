package watcher

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"claude-remote-server/internal/models"
	"claude-remote-server/internal/state"
)

// TranscriptWatcher monitors ~/.claude/projects for session activity
type TranscriptWatcher struct {
	store       *state.Store
	projectsDir string
	fileOffsets map[string]int64
	mu          sync.Mutex
	stopCh      chan struct{}
}

// NewTranscriptWatcher initializes a new file watcher
func NewTranscriptWatcher(store *state.Store) *TranscriptWatcher {
	home, err := os.UserHomeDir()
	projectsDir := ""
	if err == nil {
		projectsDir = filepath.Join(home, ".claude", "projects")
	}

	return &TranscriptWatcher{
		store:       store,
		projectsDir: projectsDir,
		fileOffsets: make(map[string]int64),
		stopCh:      make(chan struct{}),
	}
}

// Start begins periodic polling of JSONL transcript files
func (tw *TranscriptWatcher) Start() {
	if tw.projectsDir == "" {
		return
	}

	ticker := time.NewTicker(2 * time.Second)
	go func() {
		for {
			select {
			case <-ticker.C:
				tw.scan()
			case <-tw.stopCh:
				ticker.Stop()
				return
			}
		}
	}()
}

// Stop terminates the file watcher
func (tw *TranscriptWatcher) Stop() {
	close(tw.stopCh)
}

func (tw *TranscriptWatcher) scan() {
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

func (tw *TranscriptWatcher) handleTranscriptRecord(sess *models.Session, record map[string]interface{}) {
	recordType, _ := record["type"].(string)

	if recordType == "assistant" {
		var blocks []interface{}
		if msg, ok := record["message"].(map[string]interface{}); ok {
			if content, ok := msg["content"].([]interface{}); ok {
				blocks = content
			}
		} else if content, ok := record["content"].([]interface{}); ok {
			blocks = content
		}

		for _, b := range blocks {
			if block, ok := b.(map[string]interface{}); ok {
				bType, _ := block["type"].(string)
				if bType == "tool_use" {
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
				}
			}
		}
	}
}
