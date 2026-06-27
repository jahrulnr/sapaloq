import { renderMarkdown } from '../markdown';
import type { ActivityEntry } from './types';
import {
  createToolActivityElement,
  patchToolActivityElement,
  type ToolActivityMode,
} from './tool-activity';

export function renderActivityEntry(
  entry: ActivityEntry,
  mode: ToolActivityMode = 'monitor',
): HTMLElement {
  if (entry.kind === 'thinking') {
    const details = document.createElement('details');
    details.className = 'transcript-entry transcript-thinking is-collapsed';
    details.dataset.entryKind = 'thinking';
    const sum = document.createElement('summary');
    sum.textContent = '💭 thinking';
    const body = document.createElement('div');
    body.className = 'transcript-entry-body';
    body.append(renderMarkdown(entry.text));
    details.append(sum, body);
    return details;
  }
  if (entry.kind === 'text') {
    const wrap = document.createElement('div');
    wrap.className = 'transcript-entry transcript-text';
    wrap.dataset.entryKind = 'text';
    const label = document.createElement('span');
    label.className = 'transcript-entry-label';
    label.textContent = 'Assistant';
    const body = document.createElement('div');
    body.className = 'transcript-entry-body';
    body.append(renderMarkdown(entry.text));
    wrap.append(label, body);
    return wrap;
  }
  if (entry.kind === 'user') {
    const wrap = document.createElement('div');
    wrap.className = 'transcript-entry transcript-user';
    wrap.dataset.entryKind = 'user';
    const body = document.createElement('div');
    body.className = 'transcript-entry-body';
    body.append(renderMarkdown(entry.text));
    wrap.append(body);
    return wrap;
  }
  if (entry.kind === 'tool') {
    return createToolActivityElement(entry, { mode });
  }
  const wrap = document.createElement('div');
  wrap.className = 'transcript-entry transcript-status-line';
  wrap.dataset.entryKind = 'status';
  wrap.textContent = entry.label;
  return wrap;
}

export function patchActivityEntry(el: HTMLElement, entry: ActivityEntry) {
  if (entry.kind === 'text' || entry.kind === 'thinking' || entry.kind === 'user') {
    const body = el.querySelector('.transcript-entry-body');
    if (body) body.replaceChildren(renderMarkdown(entry.text));
    return;
  }
  if (entry.kind === 'tool') {
    patchToolActivityElement(el, entry);
    return;
  }
  if (entry.kind === 'status') {
    el.textContent = entry.label;
  }
}
