package state

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"claude-remote-server/internal/hooks"
	"claude-remote-server/internal/models"
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
}

// NewStore initializes an empty state store
func NewStore(port int, hostIPs []string) *Store {
	return &Store{
		sessions:      make(map[string]*models.Session),
		subagents:     make(map[string]*models.Subagent),
		notifications: make([]*models.AppNotification, 0, 100),
		subscribers:   make(map[chan models.WebSocketMessage]bool),
		port:          port,
		hostIPs:       hostIPs,
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
			ID:                payload.SessionID,
			ProjectName:       projectName,
			ProjectDir:        payload.Cwd,
			Status:            models.StatusActive,
			ActiveSubagents:   make(map[string]*models.Subagent),
			SubagentHistory:   make([]*models.Subagent, 0),
			ActiveToolIDs:     make(map[string]string),
			TranscriptPath:    payload.TranscriptPath,
			StartTime:         time.Now(),
			LastActivity:      time.Now(),
			RecentLogs:        make([]string, 0),
		}
		s.sessions[payload.SessionID] = sess
	}

	sess.LastActivity = time.Now()
	nowStr := time.Now().Format("15:04:05")

	appendLog := func(msg string) {
		sess.RecentLogs = append([]string{fmt.Sprintf("[%s] %s", nowStr, msg)}, sess.RecentLogs...)
		if len(sess.RecentLogs) > 50 {
			sess.RecentLogs = sess.RecentLogs[:50]
		}
	}

	var notifTitle, notifBody, notifType string

	switch payload.HookEventName {
	case "SessionStart":
		sess.Status = models.StatusActive
		sess.CurrentToolStatus = "Claude Code connected"
		sess.PendingQuestion = nil
		appendLog("Session connected")
		notifTitle = "Claude Code Connected"
		notifBody = fmt.Sprintf("Session active for %s", sess.ProjectName)
		notifType = "working"

	case "PreToolUse":
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
				Type:     "question",
				Title:    "Question from Claude",
				Question: qText,
				ToolName: payload.ToolName,
				Options:  options,
				AskedAt:  time.Now(),
			}
			notifTitle = "❓ Claude Code Question"
			notifBody = qText
			notifType = "permission"
		} else if payload.ToolName == "Task" || payload.ToolName == "Agent" || payload.ToolName == "invoke_subagent" {
			sess.Status = models.StatusSubagentRunning
			subID := payload.ToolUseID
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
			if sub, hasSub := sess.ActiveSubagents[payload.ToolUseID]; hasSub {
				t := time.Now()
				sub.CompletedAt = &t
				sub.Status = "completed"
				sub.CurrentToolStatus = "Completed"
				sess.SubagentHistory = append([]*models.Subagent{sub}, sess.SubagentHistory...)
				delete(sess.ActiveSubagents, payload.ToolUseID)
				delete(s.subagents, payload.ToolUseID)
				appendLog(fmt.Sprintf("Sub-agent finished: %s", sub.Description))
				notifTitle = "🤖 Sub-Agent Completed"
				notifBody = fmt.Sprintf("%s finished: %s", sub.AgentType, sub.Description)
				notifType = "subagent"
			}
		}
		if len(sess.ActiveToolIDs) == 0 && len(sess.ActiveSubagents) == 0 && sess.PendingQuestion == nil {
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
			Type:     "permission",
			Title:    fmt.Sprintf("Permission Required (%s)", payload.ToolName),
			Question: qText,
			ToolName: payload.ToolName,
			Reason:   reason,
			AskedAt:  time.Now(),
		}
		sess.CurrentToolStatus = "Waiting for permission / confirmation"
		appendLog(fmt.Sprintf("Permission requested: %s", qText))
		notifTitle = "⚠️ Permission Required"
		notifBody = qText
		notifType = "permission"

	case "SubagentStart":
		subID := payload.ToolUseID
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
		if payload.ToolUseID != "" {
			if sub, hasSub := sess.ActiveSubagents[payload.ToolUseID]; hasSub {
				t := time.Now()
				sub.CompletedAt = &t
				sub.Status = "completed"
				sub.CurrentToolStatus = "Completed"
				sess.SubagentHistory = append([]*models.Subagent{sub}, sess.SubagentHistory...)
				delete(sess.ActiveSubagents, payload.ToolUseID)
				delete(s.subagents, payload.ToolUseID)
				appendLog(fmt.Sprintf("Sub-agent completed: %s", sub.Description))
			}
		} else {
			// Complete all remaining active subagents
			t := time.Now()
			for id, sub := range sess.ActiveSubagents {
				sub.CompletedAt = &t
				sub.Status = "completed"
				sub.CurrentToolStatus = "Completed"
				sess.SubagentHistory = append([]*models.Subagent{sub}, sess.SubagentHistory...)
				delete(s.subagents, id)
			}
			sess.ActiveSubagents = make(map[string]*models.Subagent)
			appendLog("All sub-agents completed tasks")
		}
		if len(sess.ActiveSubagents) == 0 {
			sess.Status = models.StatusActive
		}

	case "Stop":
		// Mark all remaining subagents completed and remove from active map
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
		sess.CurrentTool = ""
		sess.CurrentToolStatus = "Idling (Ready for prompt)"
		sess.PendingQuestion = nil
		sess.ActiveToolIDs = make(map[string]string)
		appendLog("Turn finished (Ready for prompt)")
		notifTitle = "✅ Task Completed"
		notifBody = fmt.Sprintf("Claude Code finished working on %s and is ready for your next prompt.", sess.ProjectName)
		notifType = "task_done"

	case "SessionEnd":
		sess.Status = models.StatusCompleted
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
		if payload.NotificationType == "waiting_input" || payload.NotificationType == "permission_prompt" {
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

	// Dispatch notification if applicable
	if notifTitle != "" {
		s.AddNotification(sess.ID, notifTitle, notifBody, notifType)
	}
}

// GetSnapshot returns the complete snapshot of the server state
func (s *Store) GetSnapshot() models.ServerStateSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sessionList := make([]*models.Session, 0, len(s.sessions))
	var activeSession *models.Session
	workingState := false
	activeSubagentCount := 0

	for _, sess := range s.sessions {
		sessionList = append(sessionList, sess)
		if sess.Status == models.StatusActive || sess.Status == models.StatusSubagentRunning || sess.Status == models.StatusWaitingPermission {
			workingState = true
			if activeSession == nil || sess.LastActivity.After(activeSession.LastActivity) {
				activeSession = sess
			}
		}
		activeSubagentCount += len(sess.ActiveSubagents)
	}

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

// UpdateSubagentActivity directly updates a subagent's tool activity
func (s *Store) UpdateSubagentActivity(sessionID, subagentID, toolName, toolStatus string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sess, ok := s.sessions[sessionID]; ok {
		if sub, subOk := sess.ActiveSubagents[subagentID]; subOk {
			sub.CurrentTool = toolName
			sub.CurrentToolStatus = toolStatus
			sess.LastActivity = time.Now()
			s.broadcast(models.WebSocketMessage{
				Type:      "subagent_update",
				Data:      sub,
				Timestamp: time.Now(),
			})
		}
	}
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

// StartLivenessWatcher checks sessions every 1 second to detect task completions and prevent stuck states
func (s *Store) StartLivenessWatcher() {
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			s.checkLivenessAndAutoIdle()
		}
	}()
}

func (s *Store) checkLivenessAndAutoIdle() {
	s.mu.Lock()
	var completedSessions []*models.Session

	for _, sess := range s.sessions {
		// Only inspect sessions that are marked working
		if sess.Status == models.StatusActive || sess.Status == models.StatusSubagentRunning {
			// If Claude Code is awaiting user response/permission, do not auto-idle
			if sess.PendingQuestion != nil || sess.Status == models.StatusWaitingPermission {
				continue
			}

			// If last tool/task activity was more than 4 seconds ago
			if time.Since(sess.LastActivity) >= 4*time.Second {
				// Mark any stale running subagents as completed
				for _, sub := range sess.ActiveSubagents {
					t := time.Now()
					sub.CompletedAt = &t
					sub.Status = "completed"
					sub.CurrentToolStatus = "Completed"
					sess.SubagentHistory = append([]*models.Subagent{sub}, sess.SubagentHistory...)
				}
				sess.ActiveSubagents = make(map[string]*models.Subagent)
				sess.ActiveToolIDs = make(map[string]string)
				sess.CurrentTool = ""
				sess.CurrentToolStatus = "Idling (Ready for prompt)"
				sess.Status = models.StatusIdle
				sess.RecentLogs = append([]string{fmt.Sprintf("[%s] Turn finished (Ready for prompt)", time.Now().Format("15:04:05"))}, sess.RecentLogs...)
				if len(sess.RecentLogs) > 50 {
					sess.RecentLogs = sess.RecentLogs[:50]
				}

				completedSessions = append(completedSessions, sess)
			}
		}
	}
	s.mu.Unlock()

	// Broadcast updates and send Task Completed notifications for each auto-idled session
	for _, sess := range completedSessions {
		s.broadcast(models.WebSocketMessage{
			Type:      "session_update",
			Data:      sess,
			Timestamp: time.Now(),
		})

		s.AddNotification(
			sess.ID,
			"✅ Task Completed",
			fmt.Sprintf("Claude Code finished working on %s and is ready for your next prompt.", sess.ProjectName),
			"task_done",
		)
	}
}
