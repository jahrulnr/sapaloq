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

export function mountChatTranscript(entries: ReadonlyArray<TranscriptEntry | TranscriptEntryInput>) {
  const list = getMessageList();
  if (!list) return;
  mountTranscriptPane(list, paneState, coerceTranscriptEntries(entries), '', 'chat');
  scrollMessagesToBottom();
}

export function syncChatTranscript(entries: ReadonlyArray<TranscriptEntry | TranscriptEntryInput>) {
  const list = getMessageList();
  if (!list) return;
  syncTranscriptPane(list, paneState, coerceTranscriptEntries(entries), '', 'chat');
  scrollMessagesToBottom();
}
