// Shared type definitions used across the widget frontend modules.

export type RingState = 'idle' | 'thinking' | 'delegating' | 'needs-input';
export type ConnectionState = 'connecting' | 'connected' | 'reconnecting' | 'disconnected';
export type CommandEntry = { id: string; prefix: string; label: string; description: string; enabled: boolean };
export type StreamEvent = {
  kind: string;
  delta?: string;
  error?: string;
  status?: string;
  wait_seconds?: number;
  tool_call?: { name: string };
  task_id?: string;
  task_role?: string;
  task_status?: string;
  summary?: string;
};
export type ChatTurn = { id: number; seq: number; role: string; content: string };
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

// Streaming coalescer: accumulates delta chunks into one bubble so
// word-by-word streams (e.g. blackbox MiniMax-M3) render as natural typing
// instead of spawning a new DOM node per token.
export type StreamTarget = { el: HTMLElement; body?: HTMLElement; text: string; queue: string; typing: boolean };

// StreamRenderer holds the thinking/assistant bubbles for one turn so events
// (whether arriving live one-by-one or replayed as a batch) accumulate into the
// same DOM nodes instead of spawning a node per token.
export type StreamRenderer = { thinking: StreamTarget | null; assistant: StreamTarget | null };
