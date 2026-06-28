import {
  captureMessageScroll,
  getMessageList,
  restoreMessageScroll,
  type MessageScrollSnapshot,
} from '../ui/dom';
import {
  coerceTranscriptEntries,
  mountTranscriptPane,
  syncTranscriptPane,
  applyDeltaOps,
  flushDeltaMarkdownInPane,
  type TranscriptEntry,
  type TranscriptEntryInput,
  type TranscriptPatchOp,
  type TranscriptPaneState,
} from '../ui/transcript';

const paneState: TranscriptPaneState = { renderedEntryCount: 0 };

export function resetChatTranscriptState() {
  paneState.renderedEntryCount = 0;
}

/** Keep incremental sync aligned with the current DOM after a new user send. */
export function syncChatTranscriptStateFromDOM() {
  const pane = getMessageList()?.querySelector('.transcript-pane');
  paneState.renderedEntryCount = pane?.children.length ?? 0;
}

export function mountChatTranscript(
  entries: ReadonlyArray<TranscriptEntry | TranscriptEntryInput>,
  scrollSnapshot?: MessageScrollSnapshot,
) {
  const list = getMessageList();
  if (!list) return;
  const scroll = scrollSnapshot || captureMessageScroll(list);
  mountTranscriptPane(list, paneState, coerceTranscriptEntries(entries), '', 'chat', '', true);
  restoreMessageScroll(scroll, list);
}

export function syncChatTranscript(entries: ReadonlyArray<TranscriptEntry | TranscriptEntryInput>) {
  const list = getMessageList();
  if (!list) return;
  const scroll = captureMessageScroll(list);
  syncTranscriptPane(list, paneState, coerceTranscriptEntries(entries), '', 'chat');
  restoreMessageScroll(scroll, list);
}

let pendingSync: TranscriptEntry[] | null = null;
let syncRaf = 0;
let syncTimer: ReturnType<typeof setTimeout> | null = null;

function flushPendingSync() {
  if (syncRaf) {
    cancelAnimationFrame(syncRaf);
    syncRaf = 0;
  }
  if (syncTimer) {
    clearTimeout(syncTimer);
    syncTimer = null;
  }
  const batch = pendingSync;
  pendingSync = null;
  if (batch) syncChatTranscript(batch);
}

function wantsImmediateSync(entries: TranscriptEntry[]): boolean {
  return entries.some(
    (e) => e.kind === 'tool' || e.kind === 'thinking' || e.kind === 'status' || e.kind === 'progress',
  );
}

/** Batch text deltas; paint tools/thinking immediately; timer fallback when rAF stalls. */
export function scheduleSyncChatTranscript(entries: ReadonlyArray<TranscriptEntry | TranscriptEntryInput>) {
  pendingSync = coerceTranscriptEntries(entries);
  if (wantsImmediateSync(pendingSync)) {
    flushPendingSync();
    return;
  }
  if (!syncRaf) {
    syncRaf = requestAnimationFrame(() => {
      syncRaf = 0;
      flushPendingSync();
    });
  }
  if (!syncTimer) {
    syncTimer = setTimeout(() => {
      syncTimer = null;
      flushPendingSync();
    }, 60);
  }
}

export function flushScheduledChatTranscript() {
  flushPendingSync();
  const list = getMessageList();
  if (list) flushDeltaMarkdownInPane(list);
}

export function applyDeltaChatTranscript(ops: ReadonlyArray<TranscriptPatchOp>) {
  const list = getMessageList();
  if (!list || !ops.length) return;
  const thinkingOnly = ops.every(
    (op) => op.op === 'append_text' && !!op.entry_id && op.entry_id.includes('thinking'),
  );
  if (thinkingOnly) {
    applyDeltaOps(list, paneState, ops, 'chat');
    return;
  }
  const scroll = captureMessageScroll(list);
  applyDeltaOps(list, paneState, ops, 'chat');
  restoreMessageScroll(scroll, list);
}
