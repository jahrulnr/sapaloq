// Chat message bubbles: rendering, attachments display, the user-message
// context menu, error inline actions and assistant 👍/👎 feedback, plus the
// turn-content parser used when restoring history.
import { OpenAttachment, SubmitFeedback } from '../../wailsjs/go/main/App';
import type { PendingAttachment } from '../core/types';
import { formatBytes, getMessageList } from '../ui/dom';
import { renderMarkdown } from '../ui/markdown';
import { showImagePreview } from '../ui/image-preview';
import {
  getSessionID,
  getUserGroup,
  nextMessageSeq,
  resetMessageSeq,
  setUserGroup,
  spokenTaskIDs,
  getLastSubmittedText,
} from '../core/state';
import { copyText, deleteTurn, editText, retryTurn } from './message-actions';

let activeMessageMenu: HTMLElement | null = null;

export function appendMessage(
  className: string,
  text: string,
  groupID = getUserGroup(),
  turnID = 0,
  attachments: PendingAttachment[] = [],
) {
  const list = getMessageList();
  if (!list || !text) return;
  const item = document.createElement('div');
  item.className = `message ${className}`;
  item.dataset.seq = `${nextMessageSeq()}`;
  item.dataset.group = `${groupID}`;
  if (turnID > 0) item.dataset.turnId = `${turnID}`;
  item.dataset.rawText = text;
  item.append(renderMarkdown(text));
  if (attachments.length) item.append(renderMessageAttachments(attachments));
  if (className.includes('message--user')) wireUserMessage(item, text);
  if (className.includes('message--error')) wireErrorMessage(item);
  if (className.includes('message--assistant')) wireAssistantFeedback(item);
  list.appendChild(item);
  list.scrollTop = list.scrollHeight;
  return item;
}

function renderMessageAttachments(attachments: PendingAttachment[]) {
  const wrap = document.createElement('div');
  wrap.className = 'message-attachments';
  const badge = document.createElement('button');
  badge.type = 'button';
  badge.className = 'message-attachment-badge';
  badge.textContent = `${attachments.length} attachment${attachments.length > 1 ? 's' : ''}`;
  const list = document.createElement('div');
  list.className = 'message-attachment-list';
  list.hidden = true;
  badge.addEventListener('click', (event) => {
    event.stopPropagation();
    list.hidden = !list.hidden;
  });
  attachments.forEach((attachment) => {
    const row = document.createElement('button');
    row.type = 'button';
    row.className = 'message-attachment-row';
    const preview = attachment.dataURI && attachment.type.startsWith('image/')
      ? `<img src="${attachment.dataURI}" alt="">`
      : `<span class="attachment-file-icon">${attachment.type.startsWith('image/') ? 'IMG' : 'FILE'}</span>`;
    row.innerHTML = `${preview}<span><strong></strong><small>${formatBytes(attachment.size)} · ${attachment.type || 'file'}</small></span>`;
    const name = row.querySelector('strong');
    if (name) name.textContent = attachment.name;
    const image = row.querySelector('img');
    image?.addEventListener('click', (event) => {
      event.stopPropagation();
      if (attachment.dataURI) showImagePreview(attachment.dataURI, attachment.name);
    });
    row.addEventListener('click', (event) => {
      event.stopPropagation();
      if (attachment.path) {
        void OpenAttachment(attachment.path);
      } else if (attachment.dataURI && attachment.type.startsWith('image/')) {
        showImagePreview(attachment.dataURI, attachment.name);
      }
    });
    list.append(row);
  });
  wrap.append(badge, list);
  return wrap;
}

function decodeAttachmentMeta(encoded: string): PendingAttachment | null {
  try {
    return JSON.parse(decodeURIComponent(escape(atob(encoded)))) as PendingAttachment;
  } catch {
    return null;
  }
}

export function parseTurnContent(content: string): { text: string; attachments: PendingAttachment[] } {
  const attachments: PendingAttachment[] = [];
  const metadata = /<!--sapaloq-attachment:([A-Za-z0-9+/=]+)-->/g;
  for (const match of content.matchAll(metadata)) {
    const attachment = decodeAttachmentMeta(match[1]);
    if (attachment) attachments.push(attachment);
  }
  let text = content.replace(metadata, '');
  text = text.replace(/\n*!\[([^\]]*)\]\((data:image\/[^)]+)\)/g, (_match, name, dataURI) => {
    const existing = attachments.find((item) => item.name === name);
    if (existing) existing.dataURI = dataURI;
    else attachments.push({ name: name || 'image', type: dataURI.slice(5, dataURI.indexOf(';')), size: 0, dataURI });
    return '';
  });
  text = text.replace(/\n*--- file: ([^\n]+) \(([^)]+)\) ---[\s\S]*?--- end file: \1 ---/g, '');
  // The chip already shows the name/path, so drop the model-facing
  // "[Local file: …]" lines from the displayed bubble to avoid duplication.
  text = text.replace(/\n*\[Local file:[^\]]*\]/g, '');
  return { text: text.trim(), attachments };
}

export function clearMessages() {
  const list = getMessageList();
  if (list) list.innerHTML = '';
  activeMessageMenu = null;
  resetMessageSeq();
  setUserGroup(0);
  // The DOM is wiped (e.g. history restore renders completions from persisted
  // turns instead), so the live spoken-completion dedupe set must reset too —
  // otherwise a task spoken before the clear would be suppressed if it legitly
  // re-arrives live afterwards.
  spokenTaskIDs.clear();
}

export function closeMessageMenu() {
  activeMessageMenu?.remove();
  activeMessageMenu = null;
}

function showUserMessageMenu(item: HTMLElement) {
  const text = item.dataset.rawText || item.textContent || '';
  const turnID = Number(item.dataset.turnId || 0);
  closeMessageMenu();
  const menu = document.createElement('div');
  menu.className = 'message-menu';
  menu.innerHTML = `
    <button type="button" data-action="copy">Copy</button>
    <button type="button" data-action="edit">Edit</button>
    <button type="button" data-action="retry">Retry</button>
    <button type="button" data-action="delete">Delete</button>
  `;
  menu.querySelectorAll<HTMLButtonElement>('button').forEach((button) => {
    button.addEventListener('click', (event) => {
      event.stopPropagation();
      const action = button.dataset.action;
      if (action === 'copy') void copyText(text);
      if (action === 'edit') editText(text);
      if (action === 'retry') retryTurn(turnID);
      if (action === 'delete') deleteTurn(turnID);
      if (action !== 'delete') closeMessageMenu();
    });
  });
  item.append(menu);
  activeMessageMenu = menu;
}

function wireUserMessage(item: HTMLElement, _text: string) {
  item.tabIndex = 0;
  item.addEventListener('click', (event) => {
    if (window.getSelection()?.toString()) return;
    event.stopPropagation();
    showUserMessageMenu(item);
  });
  item.addEventListener('keydown', (event) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      showUserMessageMenu(item);
    }
  });
}

function wireErrorMessage(item: HTMLElement) {
  const actions = document.createElement('div');
  actions.className = 'message-inline-actions';
  actions.innerHTML = `<button type="button" title="Retry">↻</button><button type="button" title="Edit">Edit</button>`;
  const [retry, edit] = Array.from(actions.querySelectorAll<HTMLButtonElement>('button'));
  retry?.addEventListener('click', (event) => {
    event.stopPropagation();
    const turnID = Number(item.dataset.turnId || 0);
    if (turnID) retryTurn(turnID);
  });
  edit?.addEventListener('click', (event) => {
    event.stopPropagation();
    const groupID = item.dataset.group || '';
    const user = document.querySelector<HTMLElement>(`.message--user[data-group="${groupID}"]`);
    editText(user?.dataset.rawText || getLastSubmittedText());
  });
  item.append(actions);
}

// resolveAssistantTurnID returns the turn id to attribute feedback to. Assistant
// bubbles carry their own turn id once history is bound; while streaming the id
// may not be set yet, in which case we fall back to the latest user turn group.
function resolveAssistantTurnID(item: HTMLElement): number {
  const own = Number(item.dataset.turnId || 0);
  if (own > 0) return own;
  const groupID = item.dataset.group || '';
  const user = document.querySelector<HTMLElement>(`.message--user[data-group="${groupID}"]`);
  return Number(user?.dataset.turnId || 0);
}

// wireAssistantFeedback attaches 👍/👎 controls to an assistant bubble. 👎 opens
// an inline (optional) correction box; the correction is stored as negative
// guidance the core injects into future prompts.
export function wireAssistantFeedback(item: HTMLElement) {
  const bar = document.createElement('div');
  bar.className = 'message-feedback';
  bar.innerHTML = `
    <button type="button" class="feedback-btn" data-signal="up" title="Good response" aria-label="Good response">👍</button>
    <button type="button" class="feedback-btn" data-signal="down" title="Bad response" aria-label="Bad response">👎</button>
  `;
  const up = bar.querySelector<HTMLButtonElement>('[data-signal="up"]');
  const down = bar.querySelector<HTMLButtonElement>('[data-signal="down"]');

  const markSent = (signal: 'up' | 'down') => {
    bar.dataset.sent = signal;
    up?.classList.toggle('is-active', signal === 'up');
    down?.classList.toggle('is-active', signal === 'down');
  };

  up?.addEventListener('click', async (event) => {
    event.stopPropagation();
    const turnID = resolveAssistantTurnID(item);
    try {
      await SubmitFeedback(getSessionID(), turnID, 'up', '');
      markSent('up');
    } catch {
      // Feedback is best-effort; ignore transient core errors.
    }
  });

  down?.addEventListener('click', (event) => {
    event.stopPropagation();
    openCorrectionBox(item, bar, markSent);
  });

  item.append(bar);
}

// openCorrectionBox renders an inline textarea so the user can (optionally)
// explain what was wrong before submitting a 👎. Submitting with empty text
// still records the negative signal.
function openCorrectionBox(
  item: HTMLElement,
  bar: HTMLElement,
  markSent: (signal: 'up' | 'down') => void,
) {
  if (bar.querySelector('.feedback-correction')) return;
  const wrap = document.createElement('div');
  wrap.className = 'feedback-correction';
  wrap.innerHTML = `
    <textarea rows="2" placeholder="What should it avoid next time? (optional)"></textarea>
    <div class="feedback-correction-actions">
      <button type="button" data-action="send">Submit</button>
      <button type="button" data-action="cancel">Cancel</button>
    </div>
  `;
  const textarea = wrap.querySelector<HTMLTextAreaElement>('textarea');
  const send = wrap.querySelector<HTMLButtonElement>('[data-action="send"]');
  const cancel = wrap.querySelector<HTMLButtonElement>('[data-action="cancel"]');

  cancel?.addEventListener('click', (event) => {
    event.stopPropagation();
    wrap.remove();
  });
  send?.addEventListener('click', async (event) => {
    event.stopPropagation();
    const correction = (textarea?.value || '').trim();
    const turnID = resolveAssistantTurnID(item);
    try {
      await SubmitFeedback(getSessionID(), turnID, 'down', correction);
      markSent('down');
    } catch {
      // best-effort
    }
    wrap.remove();
  });

  bar.append(wrap);
  textarea?.focus();
}

// appendThinkingBubble re-creates a settled (collapsed, done) reasoning bubble
// from a persisted "thinking" turn so reasoning survives a restart. It mirrors
// makeThinkingTarget's markup but in its finished state (no pulse, "thought"
// label, re-expandable via the toggle).
export function appendThinkingBubble(text: string, groupID = getUserGroup()) {
  const list = getMessageList();
  if (!list || !text.trim()) return;
  const el = document.createElement('div');
  el.className = 'message message--thinking is-collapsed is-done';
  el.dataset.seq = `${nextMessageSeq()}`;
  el.dataset.group = `${groupID}`;
  el.dataset.rawText = text;

  const header = document.createElement('button');
  header.type = 'button';
  header.className = 'thinking-toggle';
  header.innerHTML = `<span class="status-pulse" aria-hidden="true"><i></i><i></i><i></i></span><span class="thinking-label">thought</span><span class="thinking-chevron" aria-hidden="true">⌄</span>`;

  const body = document.createElement('div');
  body.className = 'thinking-body';
  body.append(renderMarkdown(text));

  header.addEventListener('click', (event) => {
    event.stopPropagation();
    const expanded = el.classList.toggle('is-expanded');
    el.classList.toggle('is-collapsed', !expanded);
    header.setAttribute('aria-expanded', String(expanded));
  });

  el.append(header, body);
  list.appendChild(el);
}
