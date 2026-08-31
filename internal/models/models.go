package models

import "time"

// SessionStatus represents the operational state of a Claude Code session
type SessionStatus string

const (
	StatusActive            SessionStatus = "active"             // Claude Code is actively executing a task/tool
	StatusIdle              SessionStatus = "idle"               // Claude Code is waiting for user prompt
	StatusWaitingPermission SessionStatus = "waiting_permission" // Claude Code is waiting for user confirmation/permission/input
	StatusSubagentRunning   SessionStatus = "subagent_running"   // One or more subagents are actively working
	StatusCompleted         SessionStatus = "completed"          // Session ended
)

// Subagent represents a child or teammate agent spawned by Claude Code
type Subagent struct {
	ID                string     `json:"id"`
	ParentSessionID   string     `json:"parent_session_id"`
	AgentType         string     `json:"agent_type"` // e.g. "Task", "Agent", "General-Investigator", "coder"
	Description       string     `json:"description"`
	CurrentTool       string     `json:"current_tool"`        // e.g. "Read", "Edit", "Bash"
	CurrentToolStatus string     `json:"current_tool_status"` // e.g. "Editing internal/api/server.go"
	Status            string     `json:"status"`              // "running", "idle", "completed"
	StartedAt         time.Time  `json:"started_at"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
}

// PendingQuestion represents a question or permission approval Claude Code is currently asking the user
type PendingQuestion struct {
	Type      string    `json:"type"` // "permission", "question", "confirmation"
	Title     string    `json:"title"`
	Question  string    `json:"question"`
	ToolName  string    `json:"tool_name,omitempty"`
	ToolUseID string    `json:"tool_use_id,omitempty"` // id of the asking tool_use; its PostToolUse clears the question
	Options   []string  `json:"options,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Stale     bool      `json:"stale"` // true once the question sat unanswered past the stale window
	AskedAt   time.Time `json:"asked_at"`
}

// Session represents a tracked Claude Code session
type Session struct {
	ID                string               `json:"id"`
	ProjectName       string               `json:"project_name"`
	ProjectDir        string               `json:"project_dir"`
	Status            SessionStatus        `json:"status"`
	CurrentTool       string               `json:"current_tool"`
	CurrentToolStatus string               `json:"current_tool_status"`
	PendingQuestion   *PendingQuestion     `json:"pending_question,omitempty"`
	ActiveSubagents   map[string]*Subagent `json:"active_subagents"`
	SubagentHistory   []*Subagent          `json:"subagent_history"`
	ActiveToolIDs     map[string]string    `json:"active_tool_ids"`
	TurnActive        bool                 `json:"turn_active"` // true between UserPromptSubmit/PreToolUse and Stop
	TranscriptPath    string               `json:"transcript_path"`
	StartTime         time.Time            `json:"start_time"`
	LastActivity      time.Time            `json:"last_activity"`
	LinesProcessed    int                  `json:"lines_processed"`
	RecentLogs        []string             `json:"recent_logs"`
}

// HookPayload represents the JSON payload received from Claude Code hooks
type HookPayload struct {
	HookEventName    string                 `json:"hook_event_name"`
	SessionID        string                 `json:"session_id"`
	TranscriptPath   string                 `json:"transcript_path,omitempty"`
	Cwd              string                 `json:"cwd,omitempty"`
	ToolName         string                 `json:"tool_name,omitempty"`
	ToolUseID        string                 `json:"tool_use_id,omitempty"`
	AgentID          string                 `json:"agent_id,omitempty"` // subagent identity (SubagentStart/SubagentStop)
	ToolInput        map[string]interface{} `json:"tool_input,omitempty"`
	NotificationType string                 `json:"notification_type,omitempty"`
	AgentType        string                 `json:"agent_type,omitempty"`
	TeammateName     string                 `json:"teammate_name,omitempty"`
	Description      string                 `json:"description,omitempty"`
	Reason           string                 `json:"reason,omitempty"`
	Source           string                 `json:"source,omitempty"`
	Message          string                 `json:"message,omitempty"`
}

// AppNotification represents an alert sent to the Android APK
type AppNotification struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Type      string    `json:"type"` // "working", "idle", "permission", "subagent", "info", "task_done"
	Timestamp time.Time `json:"timestamp"`
}

// WebSocketMessage represents real-time messages sent across WebSocket connections
type WebSocketMessage struct {
	Type      string      `json:"type"` // "initial_state", "session_update", "notification", "stats"
	Data      interface{} `json:"data"`
	Timestamp time.Time   `json:"timestamp"`
}

// ServerStateSnapshot represents the complete system state sent on connection
type ServerStateSnapshot struct {
	ServerVersion string             `json:"server_version"`
	HostIPs       []string           `json:"host_ips"`
	Port          int                `json:"port"`
	ActiveSession *Session           `json:"active_session,omitempty"`
	Sessions      []*Session         `json:"sessions"`
	Notifications []*AppNotification `json:"notifications"`
	SystemSummary struct {
		TotalSessions   int  `json:"total_sessions"`
		ActiveSessions  int  `json:"active_sessions"`
		ActiveSubagents int  `json:"active_subagents"`
		WorkingState    bool `json:"working_state"`
	} `json:"system_summary"`
}
