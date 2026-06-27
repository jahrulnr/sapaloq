// Shared type definitions used across the widget frontend modules.

export type RingState = 'idle' | 'thinking' | 'delegating' | 'needs-input';
export type ConnectionState = 'connecting' | 'connected' | 'reconnecting' | 'disconnected';
export type CommandEntry = { id: string; prefix: string; label: string; description: string; enabled: boolean };

export type { TranscriptEntry, TranscriptEntryKind, TranscriptPatch } from '../ui/transcript/types';

/** Legacy watch-stream events (background task_update only). */
export type StreamEvent = {
  kind: string;
  delta?: string;
  error?: string;
  status?: string;
  wait_seconds?: number;
  tool_call?: { id?: string; name: string; arguments?: unknown; source?: string };
  tool_result?: string;
  task_id?: string;
  task_role?: string;
  task_status?: string;
  summary?: string;
  checkpoint_index?: number;
  checkpoint_reason?: string;
  checkpoint_summary?: string;
  at?: string;
};

export type ChatTurn = { id: number; seq: number; role: string; content: string; checkpoint_index?: number; archived?: boolean; created_at?: string };
export type SessionSummary = { id: string; title: string; active: boolean; turn_count: number; updated_at: string; created_at: string };
export type ChatUsage = { session_id: string; used_tokens: number; context_window: number; percent: number; provider: string; model: string };
export type ActorRuntimeStatus = { id: string; role: string; status: string; phase: string; workspace: string };
export type RuntimeStatus = {
  provider: string;
  model: string;
  driver: string;
  reasoning?: string;
  config_path: string;
  data_path: string;
  memory_path: string;
  state_path: string;
  workspace_path: string;
  actors: ActorRuntimeStatus[];
};
export type PendingAttachment = { name: string; type: string; size: number; path?: string; dataURI?: string; text?: string; isDir?: boolean };
