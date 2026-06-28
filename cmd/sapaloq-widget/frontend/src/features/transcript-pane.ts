import { getMessageList, scrollMessagesToBottom } from '../ui/dom';
import {
  coerceTranscriptEntries,
  mountTranscriptPane,
  syncTranscriptPane,
  type TranscriptEntry,
  type TranscriptEntryInput,
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

export function mountChatTranscript(entries: ReadonlyArray<TranscriptEntry | TranscriptEntryInput>) {
  const list = getMessageList();
  if (!list) return;
  mountTranscriptPane(list, paneState, coerceTranscriptEntries(entries), '', 'chat', '', true);
  scrollMessagesToBottom();
}

export function syncChatTranscript(entries: ReadonlyArray<TranscriptEntry | TranscriptEntryInput>) {
  const list = getMessageList();
  if (!list) return;
  syncTranscriptPane(list, paneState, coerceTranscriptEntries(entries), '', 'chat');
  scrollMessagesToBottom();
}
