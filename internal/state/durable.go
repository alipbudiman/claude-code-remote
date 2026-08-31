package state

// durable.go — the persistence half of hook ingestion. Everything on disk
// lives under the durable directory (production: <home>/.claude):
//
//	claude-remote-events.jsonl      append-only RAW event log; replayed at
//	                                 boot so a restart rebuilds sessions from
//	                                 the original events, never from a
//	                                 snapshot of (possibly wrong) derived state.
//	claude-remote-hook.spool.jsonl  written by the hook bridge whenever the
//	                                 server is unreachable; drained by the
//	                                 server at boot and every 10 s.
//
// Concurrency: EventLog.Append is called from Store.HandleHookEvent while the
// store mutex is held, so log order always equals state-apply order. The log's
// own mutex is a leaf (it never calls back into Store), so no lock inversion
// is possible. DrainSpool takes no store lock while reading the spool file and
// feeds events one-by-one through the public HandleHookEvent, which locks and
// unlocks internally — the drain ticker can therefore never deadlock against
// live hook ingestion.

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
)

const (
	// eventLogName is the append-only raw event log inside the durable dir.
	eventLogName = "claude-remote-events.jsonl"

	// SpoolName is the bridge-side spool file inside the durable dir. It is
	// written by the generated hook script (~/.claude/claude-remote-hook.js)
	// whenever the POST fails or times out, and drained by the server.
	SpoolName = "claude-remote-hook.spool.jsonl"

	// replayWindow is how far back boot replay reads the event log.
	replayWindow = 24 * time.Hour

	// replayMaxLines bounds boot replay to the most recent N log lines.
	replayMaxLines = 10000
)

// eventLogEntry is one line of the event log: the raw payload plus the time
// the server accepted it (the timestamp drives the 24h replay window).
type eventLogEntry struct {
	TS    time.Time          `json:"ts"`
	Event models.HookPayload `json:"event"`
}

// EventLog is the append-only raw event log under the durable directory.
type EventLog struct {
	path string
	mu   sync.Mutex
}

// NewEventLog returns an event log stored in dir. The file is created lazily
// on the first append.
func NewEventLog(dir string) *EventLog {
	return &EventLog{path: filepath.Join(dir, eventLogName)}
}

// Path returns the log file location (diagnostics).
func (l *EventLog) Path() string { return l.path }

// Append writes payload as ONE JSON line. Per-event append (no batching, no
// fsync) so an abrupt kill loses at most the event in flight.
func (l *EventLog) Append(payload models.HookPayload) error {
	line, err := json.Marshal(eventLogEntry{TS: time.Now().UTC(), Event: payload})
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return nil
}

// Replay re-feeds the event log to feed, oldest→newest, so a fresh store
// rebuilds exactly as if the events had arrived live. Only the last
// replayWindow of entries are used, bounded to the replayMaxLines most recent
// lines; malformed lines and entries without a usable timestamp are skipped.
// Every fed payload has Source forced to "replay": state transitions apply,
// but notifications are suppressed (replayed history must not re-buzz) — see
// the Source gate at the bottom of Store.HandleHookEvent.
// Replay returns the number of events fed.
func (l *EventLog) Replay(feed func(models.HookPayload)) (int, error) {
	f, err := os.Open(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()

	cutoff := time.Now().Add(-replayWindow)
	var kept []eventLogEntry // most recent replayMaxLines qualifying entries

	r := bufio.NewReader(f)
	for {
		line, readErr := r.ReadString('\n')
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			var e eventLogEntry
			if jerr := json.Unmarshal([]byte(trimmed), &e); jerr == nil &&
				!e.TS.IsZero() && !e.TS.Before(cutoff) && e.Event.HookEventName != "" {
				kept = append(kept, e)
				// Bounded memory: compact back to the most recent
				// replayMaxLines entries once well past the cap.
				if len(kept) > 2*replayMaxLines {
					kept = append([]eventLogEntry(nil), kept[len(kept)-replayMaxLines:]...)
				}
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				return 0, readErr
			}
			break
		}
	}
	if len(kept) > replayMaxLines {
		kept = kept[len(kept)-replayMaxLines:]
	}

	for _, e := range kept {
		e.Event.Source = "replay"
		feed(e.Event)
	}
	return len(kept), nil
}

// DrainSpool processes the bridge spool file: every valid line is fed to feed
// in file order, then the file is reset so the same line is never processed
// twice. Fed payloads get Source "" — these are REAL events the user never saw
// live, so they must raise notifications. Malformed lines are skipped (and
// logged to stdout). Lines appended while we were reading are preserved for
// the next drain. Returns the number of events fed.
func DrainSpool(dir string, feed func(models.HookPayload)) (int, error) {
	path := filepath.Join(dir, SpoolName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return 0, nil
	}

	processed := 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var p models.HookPayload
		if jerr := json.Unmarshal([]byte(line), &p); jerr != nil {
			fmt.Printf("⚠️  Spool drain: skipping malformed line in %s: %v\n", path, jerr)
			continue
		}
		p.Source = "" // spooled events notify like live hook events
		feed(p)
		processed++
	}

	// Reset the spool. If the bridge appended bytes beyond what we read
	// (possible only while HTTP is not yet listening), keep that tail.
	if err := resetSpool(path, int64(len(data))); err != nil {
		return processed, fmt.Errorf("truncate spool: %w", err)
	}
	return processed, nil
}

// resetSpool empties the spool file after a drain. readTo is the byte length
// the drain consumed; anything appended past it is rewritten as the file's new
// content so it survives until the next drain.
func resetSpool(path string, readTo int64) error {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // nothing left to reset
		}
		return err
	}
	if st.Size() <= readTo {
		return os.Truncate(path, 0)
	}
	// Tail appended during the drain: preserve it.
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(readTo, io.SeekStart); err != nil {
		return err
	}
	tail, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	return os.WriteFile(path, tail, 0644)
}
