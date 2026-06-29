import { applyDeltaOps, flushDeltaMarkdownInPane } from './apply-transcript-delta';
import { syncTranscriptPane } from './sync-pane';
import type { ToolActivityMode } from './tool-activity';
import type { TranscriptEntry, TranscriptPaneState, TranscriptPatch } from './types';

/** Shared live-transcript patch handler for chat + sub-agent monitor. */
export type TranscriptPatchTarget = {
  body: HTMLElement;
  state: TranscriptPaneState;
  mode: ToolActivityMode;
  emptyMessage?: string;
  emptyClass?: string;
  /** Called when a snapshot patch arrives so callers can cache entries. */
  onSnapshot?: (entries: TranscriptEntry[]) => void;
};

export function applyTranscriptPatchToTarget(target: TranscriptPatchTarget, patch: TranscriptPatch) {
  if (patch.mode === 'delta' && patch.ops?.length) {
    applyDeltaOps(target.body, target.state, patch.ops, target.mode);
    return;
  }
  if (!patch.entries?.length) return;
  target.onSnapshot?.(patch.entries);
  syncTranscriptPane(
    target.body,
    target.state,
    patch.entries,
    target.emptyMessage ?? '',
    target.mode,
    target.emptyClass ?? '',
  );
}

export function flushTranscriptPatchMarkdown(body: HTMLElement) {
  flushDeltaMarkdownInPane(body);
}
