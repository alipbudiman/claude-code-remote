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
  system_summary: {
    total_sessions: number;
    active_sessions: number;
    active_subagents: number;
    working_state: boolean;
  };
}

export interface WebSocketMessage {
  type: 'initial_state' | 'session_update' | 'subagent_update' | 'notification' | 'stats';
  data: any;
  timestamp: string;
}
