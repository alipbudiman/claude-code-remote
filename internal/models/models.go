package models

import (
	"encoding/json"
	"time"
)

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
	// LastCompletedAt is stamped by a verified turn-end (real Stop hook or
	// watcher-synthesized Stop); nil while working or after a stall fallback.
	// Drives the app's explicit "Task Completed" display (idle + set = done).
	LastCompletedAt *time.Time `json:"last_completed_at,omitempty"`
	// PromptQueueDepth counts prompts queued from the phone, delivered one
	// per turn-end via the Stop hook's decision:block continuation.
	PromptQueueDepth int    `json:"prompt_queue_depth,omitempty"`
	// ProcessEvents is the live process feed ring (newest last, capped).
	ProcessEvents []ProcessEvent `json:"process_events,omitempty"`
	TranscriptPath  string     `json:"transcript_path"`
	StartTime       time.Time  `json:"start_time"`
	LastActivity    time.Time  `json:"last_activity"`
	LinesProcessed  int        `json:"lines_processed"`
	RecentLogs      []string   `json:"recent_logs"`
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
	// Remote-interaction fields (2026-09-02). PermissionMode is the common
	// hook input field ("default"|"acceptEdits"|"plan"|"bypassPermissions"|…);
	// StopHookActive guards Stop-hook continuation chains; LastAssistantMessage
	// is Claude's final text at Stop; Prompt is the UserPromptSubmit text;
	// ToolResponse is the PostToolUse tool result; PermissionSuggestions are
	// the "always allow" options the permission dialog would offer.
	PermissionMode        string                   `json:"permission_mode,omitempty"`
	StopHookActive        *bool                    `json:"stop_hook_active,omitempty"`
	LastAssistantMessage  string                   `json:"last_assistant_message,omitempty"`
	Prompt                string                   `json:"prompt,omitempty"`
	ToolResponse          json.RawMessage          `json:"tool_response,omitempty"`
	PermissionSuggestions []map[string]interface{} `json:"permission_suggestions,omitempty"`
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
	// Remote-interaction additions (2026-09-02).
	PendingDecisions    []*PendingDecision `json:"pending_decisions,omitempty"`
	RecentProcessEvents []ProcessEvent     `json:"recent_process_events,omitempty"` // active session tail
	AppSettings         AppSettings        `json:"app_settings"`
	SystemSummary       struct {
		TotalSessions   int  `json:"total_sessions"`
		ActiveSessions  int  `json:"active_sessions"`
		ActiveSubagents int  `json:"active_subagents"`
		WorkingState    bool `json:"working_state"`
	} `json:"system_summary"`
}

// --- Live process view + remote interaction (2026-09-02) ---

// ProcessEventKind categorizes one entry of the live process feed.
type ProcessEventKind string

const (
	EventUserPrompt ProcessEventKind = "user_prompt"
	EventThinking   ProcessEventKind = "thinking"
	EventText       ProcessEventKind = "text"
	EventToolUse    ProcessEventKind = "tool_use"
	EventToolResult ProcessEventKind = "tool_result"
	EventToolError  ProcessEventKind = "tool_error"
	EventTurnEnd    ProcessEventKind = "turn_end"
)

// ProcessEvent is one step of the agent's live execution stream.
type ProcessEvent struct {
	ID        uint64           `json:"id"` // per-store monotonic sequence
	SessionID string           `json:"session_id"`
	Kind      ProcessEventKind `json:"kind"`
	ToolName  string           `json:"tool_name,omitempty"`
	ToolUseID string           `json:"tool_use_id,omitempty"`
	Title     string           `json:"title"`            // one-line summary
	Detail    string           `json:"detail,omitempty"` // full content (capped)
	Timestamp time.Time        `json:"timestamp"`
}

// QuestionOption is one selectable choice of an AskUserQuestion question.
type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// QuestionSpec is one normalized question of an AskUserQuestion call.
type QuestionSpec struct {
	Question    string           `json:"question"`
	Header      string           `json:"header,omitempty"`
	MultiSelect bool             `json:"multi_select,omitempty"`
	Options     []QuestionOption `json:"options"`
}

// PendingDecision is an approval/question/plan decision parked while the
// hook long-polls, waiting for the phone (or the timeout fallback).
type PendingDecision struct {
	ID          string                   `json:"id"`
	SessionID   string                   `json:"session_id"`
	Kind        string                   `json:"kind"` // "permission" | "question" | "plan"
	ToolName    string                   `json:"tool_name,omitempty"`
	ToolUseID   string                   `json:"tool_use_id,omitempty"`
	Title       string                   `json:"title"`
	Question    string                   `json:"question,omitempty"`
	ToolInput   map[string]interface{}  `json:"tool_input,omitempty"`
	Questions   []QuestionSpec           `json:"questions,omitempty"`     // AskUserQuestion (normalized)
	RawQuestions []interface{}           `json:"raw_questions,omitempty"` // AskUserQuestion (verbatim, for echo)
	// Suggestions holds the permission dialog's "always allow" options
	// verbatim so a chosen one can be echoed back as updatedPermissions.
	Suggestions []map[string]interface{} `json:"permission_suggestions,omitempty"`
	AskedAt     time.Time                `json:"asked_at"`
	ExpiresAt   time.Time                `json:"expires_at"`
}

// DecisionResolution is the phone's answer to a PendingDecision.
type DecisionResolution struct {
	Action     string                 `json:"action"` // allow|deny|always_allow|answer|dismiss|expire
	Answer     map[string]string      `json:"answer,omitempty"` // question text -> chosen label(s)
	Notes      string                 `json:"notes,omitempty"`
	Suggestion map[string]interface{} `json:"suggestion,omitempty"` // echoed permission_suggestions entry
	By         string                 `json:"by,omitempty"` // "phone"|"timeout"
}

// AppSettings are this server's own remote-interaction settings.
type AppSettings struct {
	ApprovalWaitS   int `json:"approval_wait_s"`   // 15..110, default 60
	LogAutoClearMin int `json:"log_autoclear_min"` // 0(off)|5|15|30
}
