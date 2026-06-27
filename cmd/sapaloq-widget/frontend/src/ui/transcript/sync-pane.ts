import type { ActivityEntry, TranscriptPaneState } from './types';
import { renderActivityEntry, patchActivityEntry } from './render';
import type { ToolActivityMode } from './tool-activity';

export function emptyTranscriptState(message: string, extraClass = ''): HTMLElement {
  const el = document.createElement('div');
  el.className = ['transcript-empty', extraClass].filter(Boolean).join(' ');
  el.textContent = message;
  return el;
}

export function createTranscriptPane(): HTMLElement {
  const pane = document.createElement('div');
  pane.className = 'transcript-pane';
  return pane;
}

export function mountTranscriptPane(
  body: HTMLElement,
  state: TranscriptPaneState,
  entries: ActivityEntry[],
  emptyMessage: string,
  mode: ToolActivityMode = 'monitor',
  emptyExtraClass = '',
) {
  body.querySelector('.transcript-pane')?.remove();
  body.querySelectorAll('.transcript-empty').forEach((node) => node.remove());

  if (entries.length === 0) {
    body.append(emptyTranscriptState(emptyMessage, emptyExtraClass));
    state.renderedEntryCount = 0;
    return;
  }
  const pane = createTranscriptPane();
  for (const entry of entries) pane.append(renderActivityEntry(entry, mode));
  body.append(pane);
  state.renderedEntryCount = entries.length;
}

export function syncTranscriptPane(
  body: HTMLElement,
  state: TranscriptPaneState,
  entries: ActivityEntry[],
  emptyMessage: string,
  mode: ToolActivityMode = 'monitor',
  emptyExtraClass = '',
) {
  let pane = body.querySelector('.transcript-pane') as HTMLElement | null;
  body.querySelectorAll('.transcript-empty').forEach((node) => node.remove());

  if (entries.length === 0) {
    pane?.remove();
    if (!body.querySelector('.transcript-empty')) {
      body.append(emptyTranscriptState(emptyMessage, emptyExtraClass));
    }
    state.renderedEntryCount = 0;
    return;
  }

  if (!pane) {
    pane = createTranscriptPane();
    body.append(pane);
    state.renderedEntryCount = 0;
  }

  const prev = state.renderedEntryCount;
  const patchEnd = Math.min(prev, entries.length);
  for (let i = 0; i < patchEnd; i++) {
    const el = pane.children[i] as HTMLElement | undefined;
    if (!el || el.dataset.entryKind !== entries[i].kind) {
      while (pane.children.length > i) pane.lastChild?.remove();
      state.renderedEntryCount = i;
      for (let j = i; j < entries.length; j++) pane.append(renderActivityEntry(entries[j], mode));
      state.renderedEntryCount = entries.length;
      return;
    }
    patchActivityEntry(el, entries[i]);
  }

  for (let i = prev; i < entries.length; i++) {
    pane.append(renderActivityEntry(entries[i], mode));
  }
  while (pane.children.length > entries.length) pane.lastChild?.remove();
  state.renderedEntryCount = entries.length;
}
