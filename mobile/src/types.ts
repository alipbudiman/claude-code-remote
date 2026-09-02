export type SessionStatus = 'active' | 'idle' | 'waiting_permission' | 'subagent_running' | 'completed';

export interface Subagent {
  id: string;
  parent_session_id: string;
  agent_type: string;
  description: string;
  current_tool: string;
  current_tool_status: string;
  status: 'running' | 'idle' | 'completed';
  started_at: string;
  completed_at?: string;
}

export interface PendingQuestion {
  type: 'permission' | 'question' | 'confirmation';
  title: string;
  question: string;
  tool_name?: string;
  options?: string[];
  reason?: string;
  asked_at: string;
}

export interface Session {
  id: string;
  project_name: string;
  project_dir: string;
  status: SessionStatus;
  current_tool: string;
  current_tool_status: string;
  pending_question?: PendingQuestion;
  active_subagents: Record<string, Subagent>;
  subagent_history: Subagent[];
  active_tool_ids: Record<string, string>;
  transcript_path: string;
  start_time: string;
  last_activity: string;
  // M9: stamped by a verified turn-end (real or watcher-synthesized Stop).
  // idle + set = the task completed and no new turn has started.
  last_completed_at?: string;
  // Remote prompts queued from the phone; delivered one per turn-end.
  prompt_queue_depth?: number;
  // Live process feed ring (newest last, server-capped).
  process_events?: ProcessEvent[];
  lines_processed: number;
  recent_logs: string[];
}

export interface AppNotification {
  id: string;
  session_id: string;
  title: string;
  body: string;
  type: 'working' | 'idle' | 'permission' | 'subagent' | 'info' | 'task_done';
  timestamp: string;
}

export interface ServerStateSnapshot {
  server_version: string;
  host_ips: string[];
  port: number;
  active_session?: Session;
  sessions: Session[];
  notifications: AppNotification[];
  // Remote-interaction additions (2026-09-02).
  pending_decisions?: PendingDecision[];
  recent_process_events?: ProcessEvent[];
  app_settings?: AppSettings;
  system_summary: {
    total_sessions: number;
    active_sessions: number;
    active_subagents: number;
    working_state: boolean;
  };
}

export interface WebSocketMessage {
  type:
    | 'initial_state' | 'session_update' | 'subagent_update' | 'notification'
    | 'stats' | 'room_status'
    // Remote-interaction frames (2026-09-02).
    | 'process_event' | 'process_sync' | 'decision_pending' | 'decision_resolved'
    | 'prompt_queued' | 'logs_cleared' | 'app_settings' | 'permissions'
    | 'client_command';
  data: any;
  timestamp: string;
}

// --- Remote interaction (2026-09-02) ---

export type ProcessEventKind =
  | 'user_prompt' | 'thinking' | 'text' | 'tool_use'
  | 'tool_result' | 'tool_error' | 'turn_end';

export interface ProcessEvent {
  id: number;
  session_id: string;
  kind: ProcessEventKind;
  tool_name?: string;
  tool_use_id?: string;
  title: string;
  detail?: string;
  timestamp: string;
}

export interface QuestionOption {
  label: string;
  description?: string;
}

export interface QuestionSpec {
  question: string;
  header?: string;
  multi_select?: boolean;
  options: QuestionOption[];
}

export interface PermissionSuggestion extends Record<string, unknown> {
  label?: string;
}

export interface PendingDecision {
  id: string;
  session_id: string;
  kind: 'permission' | 'question' | 'plan';
  tool_name?: string;
  title: string;
  question?: string;
  tool_input?: Record<string, unknown>;
  questions?: QuestionSpec[];
  permission_suggestions?: PermissionSuggestion[];
  asked_at: string;
  expires_at: string;
}

export interface AppSettings {
  approval_wait_s: number;
  log_autoclear_min: number; // 0 = off
}

export interface PermissionsConfig {
  defaultMode?: string;
  allow?: string[];
  ask?: string[];
  deny?: string[];
  additionalDirectories?: string[];
  [key: string]: unknown;
}

export interface DecisionRespondInput {
  decision_id: string;
  action: 'allow' | 'deny' | 'always_allow' | 'answer' | 'dismiss';
  answer?: Record<string, string>;
  notes?: string;
  suggestion_index?: number;
}
