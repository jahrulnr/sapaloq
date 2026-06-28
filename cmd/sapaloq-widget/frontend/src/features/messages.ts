// Chat message bubbles: rendering, attachments display, the user-message
// context menu, error inline actions and assistant 👍/👎 feedback, plus the
// turn-content parser used when restoring history.
import { OpenAttachment, SubmitFeedback } from '../../wailsjs/go/main/App';
import type { PendingAttachment } from '../core/types';
import {
  captureMessageScroll,
  formatBytes,
  getMessageList,
  hasVisibleText,
  restoreMessageScroll,
} from '../ui/dom';
import { renderMarkdown } from '../ui/markdown';
import { showImagePreview } from '../ui/image-preview';
import {
  getSessionID,
  getUserGroup,
  nextMessageSeq,
  resetMessageSeq,
  setUserGroup,
  spokenTaskIDs,
  taskBubbles,
  taskStatuses,
} from '../core/state';
import { copyText, deleteTurn, editText, retryTurn } from './message-actions';

import {
  type ToolActivityCall,
  createToolActivityElement,
  formatToolPayload,
  getToolActivityHeader,
  paintToolActivityHeader,
  patchToolActivityElement,
  setToolActivityOpen,
  toolActivityHint,
  toolEntryFromCall,
} from '../ui/transcript';

export type { ToolActivityCall };

let activeMessageMenu: HTMLElement | null = null;
const toolActivityByID = new Map<string, HTMLElement>();

export function clearToolActivityCache() {
  toolActivityByID.clear();
}

function findToolActivity(call: ToolActivityCall): HTMLElement | undefined {
  if (call.id) {
    const byID = toolActivityByID.get(call.id);
    if (byID?.isConnected) return byID;
  }
  return [...toolActivityByID.values()].reverse().find((item) =>
    item.dataset.toolName === (call.name || 'unknown') && item.dataset.complete !== 'true');
}

// appendToolActivity creates one Cursor-like activity row. The disclosure is
// the root element itself and its label is a direct text node. WebKitGTK can
// collapse a nested button to zero height when this row lands between two
// streamed thinking bubbles, leaving only the parent's borders visible.
export function appendToolActivity(call: ToolActivityCall): HTMLElement | undefined {
  const list = getMessageList();
  if (!list) return;
  const scroll = captureMessageScroll(list);
  if (call.id) {
    const existing = toolActivityByID.get(call.id);
    if (existing?.isConnected) return existing;
  }
  const item = createToolActivityElement(toolEntryFromCall(call), {
    mode: 'chat',
    extraClass: 'message',
  });
  item.dataset.seq = `${nextMessageSeq()}`;
  item.dataset.group = `${getUserGroup()}`;
  item.dataset.toolHint = toolActivityHint(call);
  const header = getToolActivityHeader(item);
  if (header) paintToolActivityHeader(item, header);
  list.append(item);
  if (call.id) toolActivityByID.set(call.id, item);
  restoreMessageScroll(scroll, list);
  return item;
}

export function completeToolActivity(call: ToolActivityCall, result: string, statusText = 'completed'): HTMLElement | undefined {
  const item = findToolActivity(call) || appendToolActivity(call);
  if (!item) return;
  patchToolActivityElement(item, {
    kind: 'tool',
    id: call.id || item.dataset.toolId || '',
    name: call.name || item.dataset.toolName || 'unknown',
    args: formatToolPayload(call.arguments) || item.querySelector('.tool-activity__section code')?.textContent || '',
    response: result,
    status: statusText,
  });
  // Collapse when done — same default as the sub-agent monitor. Header line
  // stays visible; click to expand request/response.
  setToolActivityOpen(item, false);
  const header = getToolActivityHeader(item);
  if (header) paintToolActivityHeader(item, header);
  return item;
}

type SummaryPanelOptions = {
  label: string;
  content: string;
  meta?: string;
  variant?: 'checkpoint' | 'planner';
  open?: boolean;
  taskID?: string;
  archived?: boolean;
};

export function appendSummaryPanel(options: SummaryPanelOptions): HTMLElement | undefined {
  const list = getMessageList();
  if (!list || !options.content.trim()) return;
  const scroll = captureMessageScroll(list);
  const card = document.createElement('div');
  card.className = `message summary-panel summary-panel--${options.variant || 'checkpoint'}${options.archived ? ' message--archived' : ''}`;
  card.dataset.seq = `${nextMessageSeq()}`;
  card.dataset.group = `${getUserGroup()}`;
  card.classList.toggle('is-open', options.open === true);
  card.setAttribute('role', 'button');
  card.setAttribute('tabindex', '0');
  card.setAttribute('aria-expanded', String(options.open === true));
  if (options.taskID) card.dataset.taskId = options.taskID;
  const headerText = document.createTextNode('');
  const paintHeader = () => {
    const marker = card.classList.contains('is-open') ? '−' : '+';
    headerText.nodeValue = `${marker}  ${options.label}${options.meta ? `  ·  ${options.meta}` : ''}`;
  };
  paintHeader();
  const body = document.createElement('div');
  body.className = 'summary-panel__body';
  body.hidden = options.open !== true;
  body.append(renderMarkdown(options.content));
  if (!hasVisibleText(body)) return;
  const toggle = () => {
    const open = card.classList.toggle('is-open');
    card.setAttribute('aria-expanded', String(open));
    paintHeader();
    body.hidden = !open;
  };
  card.addEventListener('click', toggle);
  card.addEventListener('keydown', (event) => {
    if (event.target !== card) return;
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      toggle();
    }
  });
  body.addEventListener('click', (event) => event.stopPropagation());
  card.append(headerText, body);
  list.append(card);
  restoreMessageScroll(scroll, list);
  return card;
}

export function appendMessage(
  className: string,
  text: string,
  groupID = getUserGroup(),
  turnID = 0,
  attachments: PendingAttachment[] = [],
) {
  const list = getMessageList();
  if (!list || !text) return;
  const scroll = captureMessageScroll(list);
  const item = document.createElement('div');
  item.className = `message ${className}`;
  item.dataset.seq = `${nextMessageSeq()}`;
  item.dataset.group = `${groupID}`;
  if (turnID > 0) item.dataset.turnId = `${turnID}`;
  item.dataset.rawText = text;
  item.append(renderMarkdown(text));
  if (attachments.length) item.append(renderMessageAttachments(attachments));
  // Restored assistant turns bypass the live stream's flush guard. Apply the
  // same meaningful-content check here so markdown-only separators or content
  // sanitized to nothing cannot leave an empty feedback bubble behind.
  if (className.includes('message--assistant') && !hasVisibleText(item)) return;
  if (className.includes('message--user')) wireUserMessage(item, text);
  if (className.includes('message--error')) wireErrorMessage(item);
  if (className.includes('message--assistant')) wireAssistantFeedback(item);
  list.appendChild(item);
  restoreMessageScroll(scroll, list);
  return item;
}

// appendCheckpointDivider inserts the visual seam between pre-checkpoint
// (archived, muted) and post-checkpoint (live) history: a horizontal rule with
// a small centered "Checkpoint n" label, followed by a collapsible summary
// card (collapsed by default so it does not dominate the chat). The summary is
// the model-authored markdown captured at compaction time; expanding it lets
// the user recall what was compacted without leaving the transcript.
export function appendCheckpointDivider(index: number, summary: string) {
  const list = getMessageList();
  if (!list) return;
  const scroll = captureMessageScroll(list);
  const divider = document.createElement('div');
  divider.className = 'checkpoint-divider';
  const ruleBefore = document.createElement('span');
  ruleBefore.className = 'checkpoint-divider__rule';
  const label = document.createElement('span');
  label.className = 'checkpoint-divider__label';
  label.textContent = `Checkpoint ${index}`;
  const ruleAfter = document.createElement('span');
  ruleAfter.className = 'checkpoint-divider__rule';
  divider.append(ruleBefore, label, ruleAfter);
  list.appendChild(divider);
  if (summary && summary.trim()) appendSummaryPanel({
    label: 'Session summary',
    meta: `Context checkpoint ${index}`,
    content: summary,
    variant: 'checkpoint',
  });
  restoreMessageScroll(scroll, list);
}

export function renderMessageAttachments(attachments: PendingAttachment[]) {
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
		let image: HTMLImageElement | null = null;
		if (attachment.dataURI && attachment.type.startsWith('image/')) {
			image = document.createElement('img');
			image.src = attachment.dataURI;
			image.alt = '';
			row.append(image);
		} else {
			const icon = document.createElement('span');
			icon.className = 'attachment-file-icon';
			icon.textContent = attachment.type.startsWith('image/') ? 'IMG' : 'FILE';
			row.append(icon);
		}
		const details = document.createElement('span');
		const name = document.createElement('strong');
		name.textContent = attachment.name;
		const meta = document.createElement('small');
		meta.textContent = `${formatBytes(attachment.size)} · ${attachment.type || 'file'}`;
		details.append(name, meta);
		row.append(details);
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
  const linkedPaths = new Set<string>();
  // Replace each attachment metadata marker with a clickable markdown link when
  // the attachment is path-backed (native file/folder drop), so a restored
  // bubble renders the same link as the live one. Pathless attachments (browser
  // /pasted) collapse to nothing here and surface via the "N attachments" badge.
  let text = content.replace(metadata, (_match, encoded) => {
    const a = decodeAttachmentMeta(encoded);
    if (a?.path) {
      linkedPaths.add(a.path);
      return `[${a.name}](${a.path})`;
    }
    return '';
  });
  text = text.replace(/\n*!\[([^\]]*)\]\((data:image\/[^)]+)\)/g, (_match, name, dataURI) => {
    const existing = attachments.find((item) => item.name === name);
    if (existing) existing.dataURI = dataURI;
    else attachments.push({ name: name || 'image', type: dataURI.slice(5, dataURI.indexOf(';')), size: 0, dataURI });
    return '';
  });
  text = text.replace(/\n*--- file: ([^\n]+) \(([^)]+)\) ---[\s\S]*?--- end file: \1 ---/g, '');
  // Backend transcript strips metadata markers but keeps model pointers like
  // `[Local folder: /path]`. Convert those to bubble links; skip when metadata
  // already produced a link for the same path.
  text = text.replace(/\[Local folder:\s*([^\]]+)\]/g, (_match, rawPath) => {
    const path = rawPath.trim();
    if (!path || linkedPaths.has(path)) return '';
    linkedPaths.add(path);
    const name = path.split('/').filter(Boolean).pop() || path;
    return `[${name}](${path})`;
  });
  text = text.replace(/\[Local file:\s*([^\]]+)\]/g, (_match, inner) => {
    const body = inner.trim();
    if (!body) return '';
    const atMatch = /^(.+?)\s+at\s+(\S+)/.exec(body);
    const path = (atMatch ? atMatch[2] : body).trim();
    if (!path || linkedPaths.has(path)) return '';
    linkedPaths.add(path);
    const name = atMatch ? atMatch[1].trim() : (path.split('/').filter(Boolean).pop() || path);
    return `[${name}](${path})`;
  });
  return { text: text.trim(), attachments };
}

export function clearMessages() {
  const list = getMessageList();
  if (list) list.innerHTML = '';
  activeMessageMenu = null;
  toolActivityByID.clear();
  resetMessageSeq();
  setUserGroup(0);
  // The DOM is wiped (e.g. history restore renders completions from persisted
  // turns instead), so the live spoken-completion dedupe set must reset too -
  // otherwise a task spoken before the clear would be suppressed if it legitly
  // re-arrives live afterwards.
  spokenTaskIDs.clear();
  taskBubbles.clear();
  taskStatuses.clear();
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

export function wireUserMessage(item: HTMLElement, _text: string) {
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

function resolveRetryTurnID(item: HTMLElement): number {
  const groupID = item.dataset.group || '';
  if (groupID) {
    const user = document.querySelector<HTMLElement>(
      `.message--user[data-group="${groupID}"], .transcript-user[data-group="${groupID}"]`,
    );
    const id = Number(user?.dataset.turnId || 0);
    if (id > 0) return id;
  }
  let prev: Element | null = item.previousElementSibling;
  while (prev) {
    if (prev instanceof HTMLElement &&
        (prev.classList.contains('message--user') || prev.classList.contains('transcript-user'))) {
      const id = Number(prev.dataset.turnId || 0);
      if (id > 0) return id;
    }
    prev = prev.previousElementSibling;
  }
  const users = document.querySelectorAll<HTMLElement>('.message--user, .transcript-user');
  return Number(users[users.length - 1]?.dataset.turnId || 0);
}

export function wireErrorMessage(item: HTMLElement) {
  let actions = item.querySelector('.message-inline-actions');
  if (!actions) {
    actions = document.createElement('div');
    actions.className = 'message-inline-actions';
    actions.innerHTML = `<button type="button" title="Retry">↻</button>`;
    item.append(actions);
  }
  const retry = actions.querySelector<HTMLButtonElement>('button');
  if (!retry || retry.dataset.retryWired === '1') return;
  retry.dataset.retryWired = '1';
  retry.addEventListener('click', (event) => {
    event.preventDefault();
    event.stopPropagation();
    const turnID = resolveRetryTurnID(item);
    if (turnID > 0) retryTurn(turnID);
  });
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
  const scroll = captureMessageScroll(list);
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
  if (!hasVisibleText(body)) return;

  header.addEventListener('click', (event) => {
    event.stopPropagation();
    const expanded = el.classList.toggle('is-expanded');
    el.classList.toggle('is-collapsed', !expanded);
    header.setAttribute('aria-expanded', String(expanded));
  });

  el.append(header, body);
  list.appendChild(el);
  restoreMessageScroll(scroll, list);
}
