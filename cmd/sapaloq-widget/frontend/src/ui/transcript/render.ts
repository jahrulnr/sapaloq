import { renderMarkdown } from '../markdown';
import { hasVisibleText } from '../dom';
import type { TranscriptEntry } from './types';
import { parseTurnContent } from '../../features/messages';
import {
  createToolActivityElement,
  patchToolActivityElement,
  type ToolActivityMode,
} from './tool-activity';
import { renderTaskCardElement, patchTaskCardElement } from './task-card';
import { wireErrorMessage } from '../../features/messages';
import { wireTranscriptEntry } from './wire';

function toolEntryFromTranscript(entry: TranscriptEntry) {
  return {
    kind: 'tool' as const,
    id: entry.tool_id || '',
    name: entry.tool_name || 'tool',
    args: entry.tool_args || '',
    response: entry.tool_result,
    status: entry.tool_status,
  };
}

export function renderTranscriptEntry(
  entry: TranscriptEntry,
  mode: ToolActivityMode = 'chat',
): HTMLElement {
  let el: HTMLElement;
  if (entry.kind === 'thinking') {
    const details = document.createElement('details');
    details.className = `transcript-entry transcript-thinking message message--thinking${entry.archived ? ' message--archived' : ''}`;
    details.dataset.entryKind = 'thinking';
    if (entry.id) details.dataset.entryId = entry.id;
    const sum = document.createElement('summary');
    sum.textContent = '💭 thinking';
    const body = document.createElement('div');
    body.className = 'transcript-entry-body thinking-body';
    body.append(renderMarkdown(entry.text || ''));
    details.append(sum, body);
    el = details;
  } else if (entry.kind === 'text') {
    const wrap = document.createElement('div');
    const planner = entry.task_role === 'planner' && entry.task_id;
    wrap.className = planner
      ? `transcript-entry summary-panel summary-panel--planner message${entry.archived ? ' message--archived' : ''}`
      : `transcript-entry transcript-text message message--assistant${entry.archived ? ' message--archived' : ''}`;
    wrap.dataset.entryKind = 'text';
    if (entry.id) wrap.dataset.entryId = entry.id;
    if (entry.task_id) wrap.dataset.taskId = entry.task_id;
    wrap.dataset.rawText = entry.text || '';
    if (planner) {
      wrap.classList.add('is-collapsed');
      wrap.setAttribute('role', 'button');
      wrap.setAttribute('tabindex', '0');
      const header = document.createElement('span');
      header.textContent = `+  Plan ready  ·  Planner · ${entry.task_id}`;
      const body = document.createElement('div');
      body.className = 'summary-panel__body transcript-entry-body';
      body.append(renderMarkdown(entry.text || ''));
      wrap.append(header, body);
    } else {
      const body = document.createElement('div');
      body.className = 'transcript-entry-body';
      body.append(renderMarkdown(entry.text || ''));
      wrap.append(body);
    }
    el = wrap;
  } else if (entry.kind === 'user') {
    const displayText = parseTurnContent(entry.text || '').text || entry.text || '';
    const wrap = document.createElement('div');
    wrap.className = `transcript-entry transcript-user message message--user${entry.archived ? ' message--archived' : ''}`;
    wrap.dataset.entryKind = 'user';
    wrap.dataset.rawText = entry.text || '';
    if (entry.id) wrap.dataset.entryId = entry.id;
    const body = document.createElement('div');
    body.className = 'transcript-entry-body';
    body.append(renderMarkdown(displayText));
    wrap.append(body);
    el = wrap;
  } else if (entry.kind === 'tool') {
    el = createToolActivityElement(toolEntryFromTranscript(entry), {
      mode,
      extraClass: mode === 'chat' ? 'message' : '',
    });
    el.dataset.entryKind = 'tool';
    if (entry.id) el.dataset.entryId = entry.id;
  } else if (entry.kind === 'task') {
    el = renderTaskCardElement(entry);
  } else if (entry.kind === 'checkpoint') {
    const wrap = document.createElement('div');
    wrap.className = 'transcript-entry transcript-checkpoint';
    wrap.dataset.entryKind = 'checkpoint';
    if (entry.id) wrap.dataset.entryId = entry.id;
    const divider = document.createElement('div');
    divider.className = 'checkpoint-divider';
    const ruleBefore = document.createElement('span');
    ruleBefore.className = 'checkpoint-divider__rule';
    const label = document.createElement('span');
    label.className = 'checkpoint-divider__label';
    label.textContent = `Checkpoint ${entry.checkpoint_index || 0}`;
    const ruleAfter = document.createElement('span');
    ruleAfter.className = 'checkpoint-divider__rule';
    divider.append(ruleBefore, label, ruleAfter);
    wrap.append(divider);
    if (entry.text?.trim()) {
      const card = document.createElement('div');
      card.className = 'message summary-panel summary-panel--checkpoint is-collapsed';
      card.setAttribute('role', 'button');
      card.setAttribute('tabindex', '0');
      card.setAttribute('aria-expanded', 'false');
      const header = document.createElement('span');
      header.textContent = `+  Session summary  ·  Context checkpoint ${entry.checkpoint_index || 0}`;
      const body = document.createElement('div');
      body.className = 'summary-panel__body';
      body.hidden = true;
      body.append(renderMarkdown(entry.text));
      const toggle = () => {
        const open = card.classList.toggle('is-open');
        card.classList.toggle('is-collapsed', !open);
        card.setAttribute('aria-expanded', String(open));
        header.textContent = `${open ? '−' : '+'}  Session summary  ·  Context checkpoint ${entry.checkpoint_index || 0}`;
        body.hidden = !open;
      };
      card.addEventListener('click', toggle);
      card.addEventListener('keydown', (event) => {
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault();
          toggle();
        }
      });
      body.addEventListener('click', (event) => event.stopPropagation());
      card.append(header, body);
      wrap.append(card);
    }
    el = wrap;
  } else if (entry.kind === 'error') {
    const wrap = document.createElement('div');
    wrap.className = `transcript-entry transcript-error message message--error${entry.archived ? ' message--archived' : ''}`;
    wrap.dataset.entryKind = 'error';
    wrap.dataset.rawText = entry.text || '';
    if (entry.id) wrap.dataset.entryId = entry.id;
    const body = document.createElement('div');
    body.className = 'transcript-entry-body';
    body.append(renderMarkdown(entry.text || ''));
    wrap.append(body);
    el = wrap;
  } else if (entry.kind === 'progress') {
    const wrap = document.createElement('div');
    wrap.className = 'transcript-entry transcript-progress message message--progress';
    wrap.dataset.entryKind = 'progress';
    wrap.dataset.progressKind = entry.label || '';
    wrap.textContent = entry.label || '';
    el = wrap;
  } else {
    const wrap = document.createElement('div');
    wrap.className = 'transcript-entry transcript-status-line';
    wrap.dataset.entryKind = 'status';
    wrap.textContent = entry.label || entry.text || '';
    el = wrap;
  }

  if (mode === 'chat') wireTranscriptEntry(el, entry);
  if (entry.kind === 'text' && !entry.task_id && !hasVisibleText(el)) {
    el.classList.add('is-empty');
  }
  return el;
}

export function patchTranscriptEntry(el: HTMLElement, entry: TranscriptEntry, _mode: ToolActivityMode = 'chat') {
  if (entry.kind === 'tool') {
    patchToolActivityElement(el, toolEntryFromTranscript(entry));
    return;
  }
  if (entry.kind === 'task') {
    patchTaskCardElement(el, entry);
    return;
  }
  if (entry.kind === 'text' || entry.kind === 'thinking' || entry.kind === 'user' || entry.kind === 'error') {
    const body = el.querySelector('.transcript-entry-body') || el;
    if (body instanceof HTMLElement && entry.text !== undefined) {
      const target = body.classList.contains('transcript-entry-body') ? body : el;
      const displayText = entry.kind === 'user'
        ? (parseTurnContent(entry.text).text || entry.text)
        : entry.text;
      target.replaceChildren(renderMarkdown(displayText));
      if (entry.kind === 'error') wireErrorMessage(el);
    }
    return;
  }
  if (entry.kind === 'status' || entry.kind === 'progress') {
    el.textContent = entry.label || entry.text || '';
  }
}

/** @deprecated use renderTranscriptEntry */
export const renderActivityEntry = renderTranscriptEntry;
/** @deprecated use patchTranscriptEntry */
export const patchActivityEntry = patchTranscriptEntry;
