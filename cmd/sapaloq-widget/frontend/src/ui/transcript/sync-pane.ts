import type { TranscriptEntry, TranscriptPaneState } from './types';
import { visibleTranscriptEntries } from './filter';
import { renderTranscriptEntry, patchTranscriptEntry } from './render';
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
  entries: TranscriptEntry[],
  emptyMessage: string,
  mode: ToolActivityMode = 'monitor',
  emptyExtraClass = '',
) {
  body.querySelector('.transcript-pane')?.remove();
  body.querySelectorAll('.transcript-empty').forEach((node) => node.remove());

  const visible = visibleTranscriptEntries(entries, mode);
  if (visible.length === 0) {
    body.append(emptyTranscriptState(emptyMessage, emptyExtraClass));
    state.renderedEntryCount = 0;
    return;
  }
  const pane = createTranscriptPane();
  for (const entry of visible) {
    const el = renderTranscriptEntry(entry, mode);
    if (!el.classList.contains('is-empty')) pane.append(el);
  }
  body.append(pane);
  state.renderedEntryCount = pane.children.length;
}

export function syncTranscriptPane(
  body: HTMLElement,
  state: TranscriptPaneState,
  entries: TranscriptEntry[],
  emptyMessage: string,
  mode: ToolActivityMode = 'monitor',
  emptyExtraClass = '',
) {
  let pane = body.querySelector('.transcript-pane') as HTMLElement | null;
  body.querySelectorAll('.transcript-empty').forEach((node) => node.remove());

  const visible = visibleTranscriptEntries(entries, mode);

  if (visible.length === 0) {
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
  const patchEnd = Math.min(prev, visible.length);
  for (let i = 0; i < patchEnd; i++) {
    const el = pane.children[i] as HTMLElement | undefined;
    if (!el || el.dataset.entryKind !== visible[i].kind) {
      while (pane.children.length > i) pane.lastChild?.remove();
      state.renderedEntryCount = i;
      for (let j = i; j < visible.length; j++) pane.append(renderTranscriptEntry(visible[j], mode));
      state.renderedEntryCount = pane.children.length;
      return;
    }
    patchTranscriptEntry(el, visible[i], mode);
  }

  for (let i = prev; i < visible.length; i++) {
    const el = renderTranscriptEntry(visible[i], mode);
    if (!el.classList.contains('is-empty')) pane.append(el);
  }
  while (pane.children.length > visible.length) pane.lastChild?.remove();
  state.renderedEntryCount = pane.children.length;
}
