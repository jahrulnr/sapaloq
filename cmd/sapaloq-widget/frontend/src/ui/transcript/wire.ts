import { parseTurnContent, wireAssistantFeedback, wireErrorMessage, wireUserMessage } from '../../features/messages';
import type { TranscriptEntry } from './types';

export function wireTranscriptEntry(el: HTMLElement, entry: TranscriptEntry) {
  if (entry.turn_id && entry.turn_id > 0) {
    el.dataset.turnId = `${entry.turn_id}`;
  }
  if (entry.archived) el.classList.add('message--archived');
  if (entry.generation_id) el.dataset.generationId = entry.generation_id;
  if (entry.id) el.dataset.entryId = entry.id;

  switch (entry.kind) {
    case 'user': {
      const parsed = parseTurnContent(entry.text || '');
      wireUserMessage(el, parsed.text || entry.text || '');
      break;
    }
    case 'text':
      wireAssistantFeedback(el);
      break;
    case 'error':
      wireErrorMessage(el);
      break;
    default:
      break;
  }
}

export function wireTranscriptPane(root: HTMLElement) {
  root.querySelectorAll<HTMLElement>('.transcript-entry').forEach((el) => {
    const kind = el.dataset.entryKind;
    if (!kind) return;
    const turnID = Number(el.dataset.turnId || 0);
    wireTranscriptEntry(el, {
      kind: kind as TranscriptEntry['kind'],
      turn_id: turnID || undefined,
      text: el.dataset.rawText,
      generation_id: el.dataset.generationId,
      archived: el.classList.contains('message--archived'),
    });
  });
}
