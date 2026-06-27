// Shared transcript entry types — used by the main chat tool rows and the
// sub-agent monitor activity pane.

export type ActivityEntry =
  | { kind: 'thinking'; text: string }
  | { kind: 'text'; text: string }
  | { kind: 'user'; text: string }
  | { kind: 'tool'; id: string; name: string; args: string; response?: string; status?: string }
  | { kind: 'status'; label: string };

/** Minimal event shape for coalescing (StreamEvent + TaskInspectEvent). */
export type StreamLikeEvent = {
  kind: string;
  delta?: string;
  error?: string;
  status?: string;
  tool_id?: string;
  tool_name?: string;
  tool_arguments?: string;
  tool_result?: string;
};

export type TranscriptPaneState = {
  renderedEntryCount: number;
};
