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
  patchTaskCardElement,
  renderTaskCardElement,
  ensureSegmentSentinel,
  prependTranscriptPane,
  setSegmentLoader,
  type TranscriptEntry,
  type TranscriptEntryInput,
  type TranscriptPatchOp,
  type TranscriptPaneState,
} from '../ui/transcript';
import { ChatHistorySegment } from '../../wailsjs/go/main/App';

const paneState: TranscriptPaneState = { renderedEntryCount: 0 };

export type TranscriptSegmentMeta = {
  has_older?: boolean;
  older_checkpoint?: number;
};

let segmentScroll = {
  hasOlder: false,
  olderCheckpoint: 0,
  loading: false,
  observer: null as IntersectionObserver | null,
};

export function resetChatTranscriptState() {
  paneState.renderedEntryCount = 0;
  resetSegmentScroll();
}

export function resetSegmentScroll(meta?: TranscriptSegmentMeta) {
  detachSegmentObserver();
  segmentScroll = {
    hasOlder: !!meta?.has_older,
    olderCheckpoint: meta?.older_checkpoint ?? 0,
    loading: false,
    observer: null,
  };
}

function detachSegmentObserver() {
  segmentScroll.observer?.disconnect();
  segmentScroll.observer = null;
}

/** Observe the top sentinel and load older compaction segments on scroll-up. */
export function setupSegmentScroll() {
  detachSegmentObserver();
  const list = getMessageList();
  const pane = list?.querySelector('.transcript-pane') as HTMLElement | null;
  if (!list || !pane || !segmentScroll.hasOlder) return;
  ensureSegmentSentinel(pane);
  const sentinel = pane.querySelector('.transcript-segment-sentinel');
  if (!sentinel) return;
  segmentScroll.observer = new IntersectionObserver(
    (entries) => {
      if (entries.some((e) => e.isIntersecting)) void loadOlderTranscriptSegment();
    },
    { root: list, rootMargin: '80px 0px 0px 0px', threshold: 0 },
  );
  segmentScroll.observer.observe(sentinel);
}

async function loadOlderTranscriptSegment() {
  if (!segmentScroll.hasOlder || segmentScroll.loading) return;
  const list = getMessageList();
  const pane = list?.querySelector('.transcript-pane') as HTMLElement | null;
  if (!list || !pane) return;
  segmentScroll.loading = true;
  setSegmentLoader(pane, true);
  try {
    const res = await ChatHistorySegment(segmentScroll.olderCheckpoint);
    prependTranscriptPane(list, paneState, coerceTranscriptEntries(res.transcript || []), 'chat');
    segmentScroll.hasOlder = !!res.has_older;
    segmentScroll.olderCheckpoint = res.older_checkpoint ?? 0;
    setupSegmentScroll();
  } catch {
    // core offline
  } finally {
    segmentScroll.loading = false;
    setSegmentLoader(pane, false);
  }
}

/** Keep incremental sync aligned with the current DOM after a new user send. */
export function syncChatTranscriptStateFromDOM() {
  const pane = getMessageList()?.querySelector('.transcript-pane');
  paneState.renderedEntryCount = pane?.children.length ?? 0;
}

/** Update task lifecycle cards in-place without remounting the foreground transcript. */
export function patchForegroundTaskCards(entries: ReadonlyArray<TranscriptEntry | TranscriptEntryInput>) {
  const list = getMessageList();
  const pane = list?.querySelector('.transcript-pane');
  if (!pane) return;
  for (const raw of coerceTranscriptEntries(entries)) {
    if (raw.kind !== 'task' || !raw.task_id) continue;
    const sel = `[data-task-id="${raw.task_id.replace(/"/g, '\\"')}"]`;
    const existing = pane.querySelector(sel) as HTMLElement | null;
    if (existing) {
      patchTaskCardElement(existing, raw);
      continue;
    }
    const el = renderTaskCardElement(raw);
    pane.append(el);
    paneState.renderedEntryCount = pane.children.length;
  }
}

export function mountChatTranscript(
  entries: ReadonlyArray<TranscriptEntry | TranscriptEntryInput>,
  scrollSnapshot?: MessageScrollSnapshot,
  segmentMeta?: TranscriptSegmentMeta,
) {
  const list = getMessageList();
  if (!list) return;
  const scroll = scrollSnapshot || captureMessageScroll(list);
  resetSegmentScroll(segmentMeta);
  mountTranscriptPane(list, paneState, coerceTranscriptEntries(entries), '', 'chat', '', true);
  const pane = list.querySelector('.transcript-pane') as HTMLElement | null;
  if (pane && segmentScroll.hasOlder) ensureSegmentSentinel(pane);
  restoreMessageScroll(scroll, list);
  setupSegmentScroll();
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
