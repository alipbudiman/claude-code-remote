package state

import (
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"claude-remote-server/internal/hooks"
	"claude-remote-server/internal/models"
)

const (
	// defaultIdleTimeout is how long a session may receive no hook events
	// before the liveness FALLBACK marks it stalled/idle. The hooks are the
	// primary signal; this only fires when tracking loses contact entirely.
	defaultIdleTimeout = 300 * time.Second

	// staleQuestionTimeout is how long a pending question may sit unanswered
	// before it is downgraded to a stale marker on an idle session.
	staleQuestionTimeout = 30 * time.Minute

	// sessionEvictionAge is how long a session may stay silent before it is
	// dropped from memory entirely.
	sessionEvictionAge = 24 * time.Hour

	// maxSnapshotSessions caps the session list handed to clients.
	maxSnapshotSessions = 20

	// toolIDCacheCap bounds the per-session seen-tool_use id set (LRU) used
	// to deduplicate JSONL-watcher replays.
	toolIDCacheCap = 500
)

// Store manages the in-memory state of Claude Code sessions, subagents, and notifications
type Store struct {
	mu            sync.RWMutex
	sessions      map[string]*models.Session
	subagents     map[string]*models.Subagent
	notifications []*models.AppNotification
	subscribers   map[chan models.WebSocketMessage]bool
	port          int
	hostIPs       []string
	idleTimeout   time.Duration

	// seenToolIDs caches recently seen tool_use ids per session so watcher
	// replays of hook-delivered events can be detected and dropped.
	seenToolIDs map[string]*toolIDCache
	// stallNotified supports anti-flap: at most one stalled notification per
	// session per idle episode (reset whenever new activity arrives).
	stallNotified map[string]bool
	// eventLog, when set, durably records every accepted hook payload (see
	// durable.go). Optional so tests and non-durable use stay untouched.
	eventLog *EventLog
	// lastEventAt is the time the most recent hook event was accepted;
	// surfaced by /api/health. Zero means "no event yet".
	lastEventAt time.Time
	// decisions holds pending remote decisions and per-session prompt
	// queues (2026-09-02); appSettings drives their behavior.
	decisions   decisionRegistry
	appSettings models.AppSettings
}

// NewStore initializes an empty state store with the default liveness timeout.
func NewStore(port int, hostIPs []string) *Store {
	return NewStoreWithIdleTimeout(port, hostIPs, defaultIdleTimeout)
}

// NewStoreWithIdleTimeout is NewStore with an explicit liveness-fallback
// timeout (how long a session may stay silent before it is marked stalled).
func NewStoreWithIdleTimeout(port int, hostIPs []string, idleTimeout time.Duration) *Store {
	if idleTimeout <= 0 {
		idleTimeout = defaultIdleTimeout
	}
	return &Store{
		sessions:      make(map[string]*models.Session),
		subagents:     make(map[string]*models.Subagent),
		notifications: make([]*models.AppNotification, 0, 100),
		subscribers:   make(map[chan models.WebSocketMessage]bool),
		port:          port,
		hostIPs:       hostIPs,
		idleTimeout:   idleTimeout,
		seenToolIDs:   make(map[string]*toolIDCache),
		stallNotified: make(map[string]bool),
		decisions:     newDecisionRegistry(),
		appSettings:   models.AppSettings{ApprovalWaitS: 60, LogAutoClearMin: 0},
	}
}

// toolIDCache is a bounded set of recently seen tool_use ids (LRU, cap
// toolIDCacheCap). It detects JSONL-watcher replays of tool_use blocks the
// hooks already delivered.
type toolIDCache struct {
	ids   map[string]struct{}
	order []string // least recently seen first
}

func newToolIDCache() *toolIDCache {
	return &toolIDCache{ids: make(map[string]struct{})}
}

func (c *toolIDCache) seen(id string) bool {
	_, ok := c.ids[id]
	return ok
}

// add records the id as most recently seen, evicting the oldest id once the
// cap is reached.
func (c *toolIDCache) add(id string) {
	if c.seen(id) {
		for i, v := range c.order {
			if v == id {
				c.order = append(c.order[:i], c.order[i+1:]...)
				break
			}
		}
		c.order = append(c.order, id)
		return
	}
	c.ids[id] = struct{}{}
	c.order = append(c.order, id)
	if len(c.order) > toolIDCacheCap {
		delete(c.ids, c.order[0])
		c.order = c.order[1:]
	}
}

// Subscribe adds a channel to receive real-time broadcast updates
func (s *Store) Subscribe() chan models.WebSocketMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan models.WebSocketMessage, 50)
	s.subscribers[ch] = true
	return ch
}

// Unsubscribe removes a broadcast channel
func (s *Store) Unsubscribe(ch chan models.WebSocketMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.subscribers, ch)
	close(ch)
}

func (s *Store) broadcast(msg models.WebSocketMessage) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ch := range s.subscribers {
		select {
		case ch <- msg:
		default:
		}
	}
}

// BroadcastStats fans out a lightweight `stats` frame (the snapshot's
// SystemSummary) to every subscriber. It exists for the server's periodic
// heartbeat (M4b): app-level clients like OkHttp never surface pong control
// frames, so they need periodic DATA traffic to tell a quiet-but-healthy link
// from a dead one without force-reconnecting mid-alert. Pure liveness +
// summary — it never mutates sessions and never raises notifications.
func (s *Store) BroadcastStats() {
	summary := s.GetSnapshot().SystemSummary
	s.broadcast(models.WebSocketMessage{
		Type:      "stats",
		Data:      summary,
		Timestamp: time.Now(),
	})
}

// AddNotification adds a notification and broadcasts it
func (s *Store) AddNotification(sessionID, title, body, notifType string) *models.AppNotification {
	s.mu.Lock()
	notif := &models.AppNotification{
		ID:        fmt.Sprintf("notif-%d", time.Now().UnixNano()),
		SessionID: sessionID,
		Title:     title,
		Body:      body,
		Type:      notifType,
		Timestamp: time.Now(),
	}
	s.notifications = append([]*models.AppNotification{notif}, s.notifications...)
	if len(s.notifications) > 100 {
		s.notifications = s.notifications[:100]
	}
	s.mu.Unlock()

	s.broadcast(models.WebSocketMessage{
		Type:      "notification",
		Data:      notif,
		Timestamp: time.Now(),
	})
	return notif
}

// GetOrCreateSession retrieves an existing session or initializes a new one
func (s *Store) GetOrCreateSession(sessionID, projectDir, transcriptPath string) *models.Session {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sess, exists := s.sessions[sessionID]; exists {
		if projectDir != "" && sess.ProjectDir == "" {
			sess.ProjectDir = projectDir
			sess.ProjectName = filepath.Base(projectDir)
		}
		if transcriptPath != "" && sess.TranscriptPath == "" {
			sess.TranscriptPath = transcriptPath
		}
		return sess
	}

	projectName := "Claude Session"
	if projectDir != "" {
		projectName = filepath.Base(projectDir)
	}

	sess := &models.Session{
		ID:                sessionID,
		ProjectName:       projectName,
		ProjectDir:        projectDir,
		Status:            models.StatusActive,
		CurrentTool:       "",
		CurrentToolStatus: "Session started",
		ActiveSubagents:   make(map[string]*models.Subagent),
		SubagentHistory:   make([]*models.Subagent, 0),
		ActiveToolIDs:     make(map[string]string),
		TranscriptPath:    transcriptPath,
		StartTime:         time.Now(),
		LastActivity:      time.Now(),
		RecentLogs:        []string{fmt.Sprintf("[%s] Session started", time.Now().Format("15:04:05"))},
	}
	s.sessions[sessionID] = sess
	return sess
}

// SetEventLog attaches the durable raw-event log. From then on every payload
// ACCEPTED by HandleHookEvent is appended as one JSON line (payloads dropped as
// watcher replay duplicates are not accepted events). Passing nil detaches.
func (s *Store) SetEventLog(l *EventLog) {
	s.mu.Lock()
	s.eventLog = l
	s.mu.Unlock()
}

// toolCacheForLocked returns (creating if needed) the per-session tool id
// cache. Caller must hold s.mu.
func (s *Store) toolCacheForLocked(sessionID string) *toolIDCache {
	cache, ok := s.seenToolIDs[sessionID]
	if !ok {
		cache = newToolIDCache()
		s.seenToolIDs[sessionID] = cache
	}
	return cache
}

// HandleHookEvent processes incoming events from Claude Code hooks
func (s *Store) HandleHookEvent(payload models.HookPayload) {
	if payload.SessionID == "" {
		payload.SessionID = "default-session"
	}

	s.mu.Lock()
	sess, exists := s.sessions[payload.SessionID]
	if !exists {
		projectName := "Claude Session"
		if payload.Cwd != "" {
			projectName = filepath.Base(payload.Cwd)
		}
		sess = &models.Session{
			ID:              payload.SessionID,
			ProjectName:     projectName,
			ProjectDir:      payload.Cwd,
			Status:          models.StatusActive,
			ActiveSubagents: make(map[string]*models.Subagent),
			SubagentHistory: make([]*models.Subagent, 0),
			ActiveToolIDs:   make(map[string]string),
			TranscriptPath:  payload.TranscriptPath,
			StartTime:       time.Now(),
			LastActivity:    time.Now(),
			RecentLogs:      make([]string, 0),
		}
		s.sessions[payload.SessionID] = sess
	}

	// M9 duplicate-Stop guard: turn completion has three sources — the real
	// Stop hook, the watcher's transcript-based turn-end (Source:"watcher"),
	// and the stall fallback. The FIRST to arrive completes the turn; any
	// later Stop on an already-idle/ended session is a full no-op (no state
	// change, no log line, no second task_done). A working session (active,
	// subagent_running, or waiting_permission — e.g. a question the user
	// dismissed before the turn ended) has not completed yet, so its Stop
	// still runs the completion path.
	if payload.HookEventName == "Stop" &&
		(sess.Status == models.StatusIdle || sess.Status == models.StatusCompleted) {
		s.mu.Unlock()
		return
	}

	sess.LastActivity = time.Now()
	// New activity ends any stall episode, re-arming the stalled notification.
	delete(s.stallNotified, payload.SessionID)
	nowStr := time.Now().Format("15:04:05")

	appendLog := func(msg string) {
		sess.RecentLogs = append([]string{fmt.Sprintf("[%s] %s", nowStr, msg)}, sess.RecentLogs...)
		if len(sess.RecentLogs) > 50 {
			sess.RecentLogs = sess.RecentLogs[:50]
		}
	}

	// Watcher redundancy dedup: the JSONL watcher may replay a tool_use the
	// hooks already delivered. A replayed id must refresh nothing and notify
	// nothing. (Ids are recorded for hook AND watcher events alike.)
	if payload.HookEventName == "PreToolUse" && payload.ToolUseID != "" {
		cache := s.toolCacheForLocked(payload.SessionID)
		alreadySeen := cache.seen(payload.ToolUseID)
		cache.add(payload.ToolUseID)
		if payload.Source == "watcher" && alreadySeen {
			s.mu.Unlock()
			return
		}
	}

	// Durable raw-event log: record every accepted payload exactly once —
	// EXCEPT replayed ones: a replayed event is already in the log (it was
	// read from there), and re-appending it with a fresh timestamp would
	// duplicate up to 10k lines per restart and keep ancient events inside
	// the 24h replay window forever. Appended while holding s.mu so the log
	// order always equals state-apply order; the log's own mutex is a leaf,
	// so no lock inversion is possible.
	if s.eventLog != nil && payload.Source != "replay" {
		if err := s.eventLog.Append(payload); err != nil {
			log.Printf("event log append failed: %v", err)
		}
	}

	// /api/health's last_event_at: stamp every accepted event (the watcher
	// replay-dup return above already exited, so this really is accepted).
	s.lastEventAt = time.Now()

	var notifTitle, notifBody, notifType string

	switch payload.HookEventName {
	case "SessionStart":
		sess.Status = models.StatusActive
		sess.TurnActive = true
		sess.CurrentToolStatus = "Claude Code connected"
		sess.PendingQuestion = nil
		appendLog("Session connected")
		notifTitle = "Claude Code Connected"
		notifBody = fmt.Sprintf("Session active for %s", sess.ProjectName)
		notifType = "working"

	case "UserPromptSubmit":
		// Turn start: the user just submitted a prompt. This is not
		// notification-worthy (the user did it themselves).
		sess.Status = models.StatusActive
		sess.TurnActive = true
		sess.PendingQuestion = nil
		sess.LastCompletedAt = nil
		sess.CurrentToolStatus = "Working on your prompt…"
		appendLog("User prompt submitted")

	case "PreToolUse":
		sess.TurnActive = true
		sess.LastCompletedAt = nil
		sess.CurrentTool = payload.ToolName
		statusDesc := hooks.FormatToolStatus(payload.ToolName, payload.ToolInput)
		sess.CurrentToolStatus = statusDesc
		if payload.ToolUseID != "" {
			sess.ActiveToolIDs[payload.ToolUseID] = statusDesc
		}
		appendLog(statusDesc)

		// Check if tool is AskUserQuestion / ask_question
		if payload.ToolName == "AskUserQuestion" || payload.ToolName == "ask_question" {
			sess.Status = models.StatusWaitingPermission
			qText := "Claude Code is asking for your input"
			var options []string

			if payload.ToolInput != nil {
				if q, ok := payload.ToolInput["question"].(string); ok && q != "" {
					qText = q
				} else if qs, ok := payload.ToolInput["questions"].([]interface{}); ok && len(qs) > 0 {
					if firstQ, ok := qs[0].(map[string]interface{}); ok {
						if qStr, ok := firstQ["question"].(string); ok {
							qText = qStr
						}
						if opts, ok := firstQ["options"].([]interface{}); ok {
							for _, o := range opts {
								if s, ok := o.(string); ok {
									options = append(options, s)
								}
							}
						}
					}
				}
			}

			sess.PendingQuestion = &models.PendingQuestion{
				Type:      "question",
				Title:     "Question from Claude",
				Question:  qText,
				ToolName:  payload.ToolName,
				ToolUseID: payload.ToolUseID,
				Options:   options,
				AskedAt:   time.Now(),
			}
			notifTitle = "❓ Claude Code Question"
			notifBody = qText
			notifType = "permission"
		} else if payload.ToolName == "ExitPlanMode" || payload.ToolName == "exit_plan_mode" {
			sess.Status = models.StatusWaitingPermission
			qText := "Claude Code has completed the implementation plan and is requesting your review/approval to begin execution."
			sess.PendingQuestion = &models.PendingQuestion{
				Type:      "question",
				Title:     "📋 Plan Ready for Review (ExitPlanMode)",
				Question:  qText,
				ToolName:  payload.ToolName,
				ToolUseID: payload.ToolUseID,
				AskedAt:   time.Now(),
			}
			notifTitle = "📋 Plan Ready for Review (ExitPlanMode)"
			notifBody = "Claude Code completed planning and is waiting for your review in the terminal."
			notifType = "permission"
		} else if payload.ToolName == "Task" || payload.ToolName == "Agent" || payload.ToolName == "invoke_subagent" {
			sess.Status = models.StatusSubagentRunning
			// Prefer the subagent's own identity; fall back to the tool_use id.
			subID := payload.AgentID
			if subID == "" {
				subID = payload.ToolUseID
			}
			if subID == "" {
				subID = fmt.Sprintf("sub-%d", time.Now().UnixNano())
			}
			desc := hooks.FormatToolStatus(payload.ToolName, payload.ToolInput)
			agentType := "Task-Agent"
			if payload.AgentType != "" {
				agentType = payload.AgentType
			}
			sub := &models.Subagent{
				ID:                subID,
				ParentSessionID:   sess.ID,
				AgentType:         agentType,
				Description:       desc,
				CurrentTool:       payload.ToolName,
				CurrentToolStatus: "Starting subtask",
				Status:            "running",
				StartedAt:         time.Now(),
			}
			sess.ActiveSubagents[subID] = sub
			s.subagents[subID] = sub
			notifTitle = "🤖 Sub-Agent Launched"
			notifBody = fmt.Sprintf("Sub-agent running: %s", desc)
			notifType = "subagent"
		} else {
			sess.Status = models.StatusActive
			sess.PendingQuestion = nil
		}

	case "PostToolUse", "PostToolUseFailure":
		if payload.ToolUseID != "" {
			delete(sess.ActiveToolIDs, payload.ToolUseID)
			// Same identity preference the subagent was keyed with.
			subKey := payload.AgentID
			if subKey == "" {
				subKey = payload.ToolUseID
			}
			if sub, hasSub := sess.ActiveSubagents[subKey]; hasSub {
				t := time.Now()
				sub.CompletedAt = &t
				sub.Status = "completed"
				sub.CurrentToolStatus = "Completed"
				sess.SubagentHistory = append([]*models.Subagent{sub}, sess.SubagentHistory...)
				delete(sess.ActiveSubagents, subKey)
				delete(s.subagents, subKey)
				appendLog(fmt.Sprintf("Sub-agent finished: %s", sub.Description))
				notifTitle = "🤖 Sub-Agent Completed"
				notifBody = fmt.Sprintf("%s finished: %s", sub.AgentType, sub.Description)
				notifType = "subagent"
			}
		}
		// The asking tool completed: the question was answered at the PC.
		if sess.PendingQuestion != nil && payload.ToolUseID != "" && payload.ToolUseID == sess.PendingQuestion.ToolUseID {
			sess.PendingQuestion = nil
		}
		if len(sess.ActiveToolIDs) == 0 && len(sess.ActiveSubagents) == 0 && sess.PendingQuestion == nil {
			if sess.TurnActive {
				// Restores a session that was idled mid-turn (e.g. by the
				// old 4s engine or a watcher gap): the turn is still open.
				sess.Status = models.StatusActive
			}
			sess.CurrentTool = ""
			sess.CurrentToolStatus = "Processing results"
		}
		if len(sess.ActiveSubagents) == 0 && sess.Status == models.StatusSubagentRunning {
			sess.Status = models.StatusActive
		}

	case "PermissionRequest":
		sess.Status = models.StatusWaitingPermission
		qText := "Claude Code is requesting permission to execute an action"
		reason := payload.Reason
		if payload.ToolInput != nil {
			if cmd, ok := payload.ToolInput["command"].(string); ok && cmd != "" {
				qText = fmt.Sprintf("Execute command: %s", cmd)
			} else if fp, ok := payload.ToolInput["file_path"].(string); ok && fp != "" {
				qText = fmt.Sprintf("Modify file: %s", fp)
			}
		}
		if payload.Message != "" {
			qText = payload.Message
		}

		sess.PendingQuestion = &models.PendingQuestion{
			Type:      "permission",
			Title:     fmt.Sprintf("Permission Required (%s)", payload.ToolName),
			Question:  qText,
			ToolName:  payload.ToolName,
			ToolUseID: payload.ToolUseID,
			Reason:    reason,
			AskedAt:   time.Now(),
		}
		sess.CurrentToolStatus = "Waiting for permission / confirmation"
		appendLog(fmt.Sprintf("Permission requested: %s", qText))
		notifTitle = "⚠️ Permission Required"
		notifBody = qText
		notifType = "permission"

	case "SubagentStart":
		// Key by the subagent's own identity when provided.
		subID := payload.AgentID
		if subID == "" {
			subID = payload.ToolUseID
		}
		if subID == "" {
			subID = fmt.Sprintf("subagent-%d", time.Now().UnixNano())
		}
		agentType := payload.AgentType
		if agentType == "" {
			agentType = payload.TeammateName
		}
		if agentType == "" {
			agentType = "Sub-Agent"
		}
		desc := payload.Description
		if desc == "" {
			desc = "Executing sub-task"
		}
		sub := &models.Subagent{
			ID:                subID,
			ParentSessionID:   sess.ID,
			AgentType:         agentType,
			Description:       desc,
			CurrentTool:       payload.ToolName,
			CurrentToolStatus: "Active",
			Status:            "running",
			StartedAt:         time.Now(),
		}
		sess.ActiveSubagents[subID] = sub
		s.subagents[subID] = sub
		sess.Status = models.StatusSubagentRunning
		appendLog(fmt.Sprintf("Sub-agent launched: %s (%s)", agentType, desc))
		notifTitle = "🤖 Sub-Agent Active"
		notifBody = fmt.Sprintf("%s is working on: %s", agentType, desc)
		notifType = "subagent"

	case "SubagentStop", "TaskCompleted", "TeammateIdle":
		// Complete EXACTLY the subagent identified by agent_id (fallback
		// tool_use_id). Completing ALL remaining subagents lives only under
		// Stop — an unidentified SubagentStop must not clobber parallel agents.
		subID := payload.AgentID
		if subID == "" {
			subID = payload.ToolUseID
		}
		if subID != "" {
			if sub, hasSub := sess.ActiveSubagents[subID]; hasSub {
				t := time.Now()
				sub.CompletedAt = &t
				sub.Status = "completed"
				sub.CurrentToolStatus = "Completed"
				sess.SubagentHistory = append([]*models.Subagent{sub}, sess.SubagentHistory...)
				delete(sess.ActiveSubagents, subID)
				delete(s.subagents, subID)
				appendLog(fmt.Sprintf("Sub-agent completed: %s", sub.Description))
			}
		}
		if len(sess.ActiveSubagents) == 0 && sess.TurnActive {
			sess.Status = models.StatusActive
		}

	case "Stop":
		// Turn finished (real hook or watcher-synthesized turn-end — the
		// duplicate-Stop guard above guarantees a working session): complete
		// all remaining subagents and retire tools. The explicit completed
		// display also drives the FGS ongoing notification, so the shade
		// says the task COMPLETED instead of a generic "idling".
		t := time.Now()
		for id, sub := range sess.ActiveSubagents {
			sub.CompletedAt = &t
			sub.Status = "completed"
			sub.CurrentToolStatus = "Completed"
			sess.SubagentHistory = append([]*models.Subagent{sub}, sess.SubagentHistory...)
			delete(s.subagents, id)
		}
		sess.ActiveSubagents = make(map[string]*models.Subagent)
		sess.Status = models.StatusIdle
		sess.TurnActive = false
		sess.CurrentTool = ""
		sess.CurrentToolStatus = "✅ Task completed"
		sess.PendingQuestion = nil
		sess.ActiveToolIDs = make(map[string]string)
		sess.LastCompletedAt = &t
		appendLog("Turn finished (task completed)")
		notifTitle = "✅ Task Completed"
		notifBody = fmt.Sprintf("Claude Code finished working on %s and is ready for your next prompt.", sess.ProjectName)
		notifType = "task_done"

	case "SessionEnd":
		sess.Status = models.StatusCompleted
		sess.TurnActive = false
		sess.CurrentToolStatus = "Session ended"
		sess.PendingQuestion = nil
		appendLog("Session closed")
		notifTitle = "Session Ended"
		notifBody = fmt.Sprintf("Session %s closed", sess.ProjectName)
		notifType = "idle"

	case "Notification":
		msg := payload.Message
		if msg == "" {
			msg = payload.NotificationType
		}
		appendLog(fmt.Sprintf("Notification: %s", msg))
		// Permission-style alerts: explicit types or the standard phrasings.
		lowerMsg := strings.ToLower(msg)
		permissionAlert := payload.NotificationType == "waiting_input" ||
			payload.NotificationType == "permission_prompt" ||
			payload.NotificationType == "agent_needs_input" ||
			strings.Contains(lowerMsg, "permission") ||
			strings.Contains(lowerMsg, "needs your input")
		if permissionAlert {
			notifTitle = "⚠️ User Input Required"
			notifType = "permission"
		} else {
			notifTitle = "Claude Code Notification"
			notifType = "info"
		}
		notifBody = msg
	}

	s.mu.Unlock()

	// Broadcast updated session state to WebSocket clients
	s.broadcast(models.WebSocketMessage{
		Type:      "session_update",
		Data:      sess,
		Timestamp: time.Now(),
	})

	// The watcher and the boot replay are redundancy/history channels: they
	// may backfill state, but they must never raise heads-up notifications —
	// with ONE exception (M9): the watcher-synthesized Stop is a completion
	// source in its own right (watcher-tracked sessions have no hook channel
	// to deliver the task_done alert), so it must notify. The duplicate-Stop
	// guard keeps that exactly once per turn. Boot replay (Source "replay")
	// still never notifies, and live hook events (Source "") always do —
	// this also covers spool drain, which feeds real events with Source "".
	if notifTitle != "" &&
		(payload.Source == "" || (payload.Source == "watcher" && payload.HookEventName == "Stop")) {
		s.AddNotification(sess.ID, notifTitle, notifBody, notifType)
	}
}

// GetSnapshot returns the complete snapshot of the server state
func (s *Store) GetSnapshot() models.ServerStateSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Deterministic, bounded session list: sessions silent for over 24h are
	// excluded, the rest are ordered most-recent-activity-first (max 20).
	evictCutoff := time.Now().Add(-sessionEvictionAge)
	sessionList := make([]*models.Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		if sess.LastActivity.Before(evictCutoff) {
			continue
		}
		sessionList = append(sessionList, sess)
	}
	sort.Slice(sessionList, func(i, j int) bool {
		return sessionList[i].LastActivity.After(sessionList[j].LastActivity)
	})
	if len(sessionList) > maxSnapshotSessions {
		sessionList = sessionList[:maxSnapshotSessions]
	}

	var activeSession *models.Session
	workingState := false
	activeSubagentCount := 0

	for _, sess := range sessionList {
		if sess.Status == models.StatusActive || sess.Status == models.StatusSubagentRunning || sess.Status == models.StatusWaitingPermission {
			workingState = true
			if activeSession == nil || sess.LastActivity.After(activeSession.LastActivity) {
				activeSession = sess
			}
		}
		activeSubagentCount += len(sess.ActiveSubagents)
	}

	// Deterministic fallback when no session is working: the most recent
	// activity, never map order.
	if activeSession == nil && len(sessionList) > 0 {
		activeSession = sessionList[0]
	}

	notifs := make([]*models.AppNotification, len(s.notifications))
	copy(notifs, s.notifications)

	snap := models.ServerStateSnapshot{
		ServerVersion: "1.0.0",
		HostIPs:       s.hostIPs,
		Port:          s.port,
		ActiveSession: activeSession,
		Sessions:      sessionList,
		Notifications: notifs,
	}

	snap.SystemSummary.TotalSessions = len(sessionList)
	snap.SystemSummary.ActiveSessions = 0
	for _, sess := range sessionList {
		if sess.Status != models.StatusCompleted {
			snap.SystemSummary.ActiveSessions++
		}
	}
	snap.SystemSummary.ActiveSubagents = activeSubagentCount
	snap.SystemSummary.WorkingState = workingState

	return snap
}

// LastEventAt returns the time the most recent hook event was accepted
// (zero if none has arrived yet). Guarded by the store mutex.
func (s *Store) LastEventAt() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastEventAt
}

// GetAllSubagents returns all active & historical subagents
func (s *Store) GetAllSubagents() []*models.Subagent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]*models.Subagent, 0)
	for _, sess := range s.sessions {
		for _, sub := range sess.ActiveSubagents {
			list = append(list, sub)
		}
		for _, sub := range sess.SubagentHistory {
			list = append(list, sub)
		}
	}
	return list
}

// StartLivenessWatcher runs the 1s liveness fallback loop: stale-question
// downgrades, stalled sessions, and 24h session eviction.
func (s *Store) StartLivenessWatcher() {
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			s.checkLivenessAndAutoIdle()
		}
	}()
}

// checkLivenessAndAutoIdle is the FALLBACK engine. Hooks are the primary
// signal: a session is only marked stalled when it has NO in-flight tools, NO
// pending question, and has been silent for the whole idle timeout.
func (s *Store) checkLivenessAndAutoIdle() {
	s.mu.Lock()
	now := time.Now()

	type stalledSession struct {
		sess   *models.Session
		mins   int
		notify bool
	}
	var stalled []stalledSession
	var updated []*models.Session

	for id, sess := range s.sessions {
		// Session hygiene: sessions silent for a day are dropped entirely.
		if now.Sub(sess.LastActivity) >= sessionEvictionAge {
			for subID := range sess.ActiveSubagents {
				delete(s.subagents, subID)
			}
			for _, sub := range sess.SubagentHistory {
				delete(s.subagents, sub.ID)
			}
			delete(s.sessions, id)
			delete(s.seenToolIDs, id)
			delete(s.stallNotified, id)
			continue
		}

		// A question unanswered for half an hour is stale, not pending.
		if sess.PendingQuestion != nil && sess.Status != models.StatusIdle &&
			now.Sub(sess.LastActivity) >= staleQuestionTimeout {
			sess.Status = models.StatusIdle
			sess.PendingQuestion.Stale = true
			sess.RecentLogs = append([]string{fmt.Sprintf("[%s] Question unanswered for 30m — marked stale", now.Format("15:04:05"))}, sess.RecentLogs...)
			if len(sess.RecentLogs) > 50 {
				sess.RecentLogs = sess.RecentLogs[:50]
			}
			updated = append(updated, sess)
		}

		// Only working sessions can stall.
		if sess.Status != models.StatusActive && sess.Status != models.StatusSubagentRunning {
			continue
		}
		// 1. A registered tool_use is in flight — only its PostToolUse /
		//    PostToolUseFailure / Stop retires it. NEVER auto-idle here.
		if len(sess.ActiveToolIDs) > 0 {
			continue
		}
		// 2. Waiting on the user, not stalled.
		if sess.PendingQuestion != nil {
			continue
		}
		// 3. Still inside the idle window.
		if now.Sub(sess.LastActivity) < s.idleTimeout {
			continue
		}

		// Stall fallback: same cleanup as a completed turn, but the
		// notification is a "stalled" warning — never a false task_done.
		silentFor := now.Sub(sess.LastActivity)
		mins := int((silentFor + time.Minute - 1) / time.Minute)
		if mins < 1 {
			mins = 1
		}
		for subID, sub := range sess.ActiveSubagents {
			t := now
			sub.CompletedAt = &t
			sub.Status = "completed"
			sub.CurrentToolStatus = "Completed"
			sess.SubagentHistory = append([]*models.Subagent{sub}, sess.SubagentHistory...)
			delete(s.subagents, subID)
		}
		sess.ActiveSubagents = make(map[string]*models.Subagent)
		sess.ActiveToolIDs = make(map[string]string)
		sess.CurrentTool = ""
		sess.CurrentToolStatus = "Idling (Ready for prompt)"
		sess.Status = models.StatusIdle
		sess.RecentLogs = append([]string{fmt.Sprintf("[%s] No events for %dm — fell back to idle (tracking may be stale)", now.Format("15:04:05"), mins)}, sess.RecentLogs...)
		if len(sess.RecentLogs) > 50 {
			sess.RecentLogs = sess.RecentLogs[:50]
		}

		// Anti-flap: at most ONE stalled notification per idle episode.
		notify := !s.stallNotified[id]
		s.stallNotified[id] = true
		stalled = append(stalled, stalledSession{sess: sess, mins: mins, notify: notify})
	}
	s.mu.Unlock()

	for _, sess := range updated {
		s.broadcast(models.WebSocketMessage{
			Type:      "session_update",
			Data:      sess,
			Timestamp: time.Now(),
		})
	}

	for _, st := range stalled {
		s.broadcast(models.WebSocketMessage{
			Type:      "session_update",
			Data:      st.sess,
			Timestamp: time.Now(),
		})
		if st.notify {
			s.AddNotification(
				st.sess.ID,
				fmt.Sprintf("⚠️ No Events for %dm", st.mins),
				fmt.Sprintf("Tracking lost contact with Claude Code for %s. Status may be outdated.", st.sess.ProjectName),
				"stalled",
			)
		}
	}
}
