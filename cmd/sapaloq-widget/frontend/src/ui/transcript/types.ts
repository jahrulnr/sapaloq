// BE-driven transcript entry types — mirror bridge.TranscriptEntry.

export type TranscriptEntryKind =
  | 'user'
  | 'thinking'
  | 'text'
  | 'tool'
  | 'status'
  | 'task'
  | 'checkpoint'
  | 'error'
  | 'progress';

export type TranscriptEntry = {
  id?: string;
  kind: TranscriptEntryKind;
  generation_id?: string;
  turn_id?: number;
  seq?: number;
  at?: string;
  archived?: boolean;
  text?: string;
  tool_id?: string;
  tool_name?: string;
  tool_args?: string;
  tool_result?: string;
  tool_status?: string;
  task_id?: string;
  task_role?: string;
  task_status?: string;
  summary?: string;
  checkpoint_index?: number;
  checkpoint_reason?: string;
  label?: string;
  wait_seconds?: number;
};

/** @deprecated Use TranscriptEntry */
export type ActivityEntry =
  | { kind: 'thinking'; text: string }
  | { kind: 'text'; text: string }
  | { kind: 'user'; text: string }
  | { kind: 'tool'; id: string; name: string; args: string; response?: string; status?: string }
  | { kind: 'status'; label: string };

export type TranscriptPaneState = {
  renderedEntryCount: number;
};

export type TranscriptPatch = {
  session_id?: string;
  generation_id?: string;
  entries?: TranscriptEntry[];
  finished?: boolean;
  turn_id?: number;
  /** BE signals the widget to discard the current transcript and render entries. */
  reset?: boolean;
  usage?: {
    used_tokens?: number;
    context_window?: number;
    percent?: number;
    provider?: string;
    model?: string;
  };
};

/** Wails/JSON payloads use plain string for kind — coerce at the UI boundary. */
export type TranscriptEntryInput = Omit<TranscriptEntry, 'kind'> & { kind: string };

export function coerceTranscriptEntries(entries: ReadonlyArray<TranscriptEntryInput>): TranscriptEntry[] {
  return entries.map((entry) => ({ ...entry, kind: entry.kind as TranscriptEntryKind }));
}
