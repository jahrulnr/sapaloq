import './style.css';
import { ChatHistory, ContextUsage, DeleteChatTurn, OpenAttachment, PingCore, ReadDroppedFile, RetryChatTurn, SendMessage, SlashSuggest, StopChat } from '../wailsjs/go/main/App';
import { EventsOn, OnFileDrop } from '../wailsjs/runtime/runtime';
import { cyclePanelSize, initWindowLayout, isExpanded, setExpanded, toggleExpanded } from './window-layout';

type RingState = 'idle' | 'thinking' | 'delegating' | 'needs-input';
type ConnectionState = 'connecting' | 'connected' | 'reconnecting' | 'disconnected';
type CommandEntry = { id: string; prefix: string; label: string; description: string; enabled: boolean };
type StreamEvent = { kind: string; delta?: string; error?: string; status?: string; tool_call?: { name: string } };
type ChatTurn = { id: number; seq: number; role: string; content: string };
type ChatUsage = { session_id: string; used_tokens: number; context_window: number; percent: number; provider: string; model: string };
type PendingAttachment = { name: string; type: string; size: number; path?: string; dataURI?: string; text?: string };

const states: RingState[] = ['idle', 'thinking', 'delegating', 'needs-input'];
let stateIndex = 0;
let lastLatencyMs = -1;
let connection: ConnectionState = 'connecting';
let pingTimer: ReturnType<typeof setInterval> | null = null;
let submitting = false;
let currentSessionID = '';
let pendingAttachments: PendingAttachment[] = [];
let messageSeq = 0;
let currentUserGroup = 0;
let lastSubmittedText = '';
let activeMessageMenu: HTMLElement | null = null;
let activeProgressBubble: HTMLElement | null = null;

function activeSlashAtChat(value: string, caret: number): { query: string; slashIndex: number } | null {
  const before = value.slice(0, caret);
  const slashIndex = before.lastIndexOf('/');
  if (slashIndex < 0) return null;
  const boundary = before.slice(Math.max(0, slashIndex - 1), slashIndex + 1);
  if (slashIndex > 0 && !SLASH_BOUNDARY.test(boundary)) return null;
  const afterSlash = before.slice(slashIndex + 1);
  if (/\s/.test(afterSlash)) return null;
  return { query: afterSlash, slashIndex };
}

function setRingState(next: RingState) {
  const orb = document.getElementById('orb');
  if (orb) orb.dataset.state = next;
}

function setConnection(state: ConnectionState) {
  connection = state;
  const dot = document.getElementById('conn-dot');
  if (dot) dot.dataset.state = state;
}

function renderRingBadge() {
  const badge = document.getElementById('ring-badge');
  if (!badge) return;
  if (lastLatencyMs < 0) {
    badge.textContent = '';
    return;
  }
  badge.textContent = `${lastLatencyMs}ms`;
}

function formatTokens(value: number) {
  if (value >= 1000) return `${Math.round(value / 1000)}k`;
  return `${value}`;
}

function renderUsage(usage?: ChatUsage | null) {
  const el = document.getElementById('context-usage');
  if (!el || !usage) return;
  const text = `${formatTokens(usage.used_tokens)}/${formatTokens(usage.context_window)}`;
  el.textContent = text;
  el.title = `${usage.percent}% context used · ${usage.provider || 'provider'} ${usage.model || ''}`.trim();
  el.dataset.level = usage.percent >= 80 ? 'danger' : usage.percent >= 70 ? 'warn' : 'normal';
}

function cycleRingState() {
  stateIndex = (stateIndex + 1) % states.length;
  setRingState(states[stateIndex]);
}

function sanitizeDisplayText(text: string) {
  return text.replace(/[\p{Extended_Pictographic}\uFE0F]/gu, '').replace(/\s+$/g, '');
}

function parseInlineMarkdown(text: string): DocumentFragment {
  const fragment = document.createDocumentFragment();
  const safeText = sanitizeDisplayText(text);
  const pattern = /(!\[[^\]]*\]\((?:https?:\/\/|data:image\/)[^)]+\)|`[^`]+`|\*\*[^*]+\*\*|\*[^*]+\*|\[[^\]]+\]\((https?:\/\/[^\s)]+)\))/g;
  let cursor = 0;
  for (const match of safeText.matchAll(pattern)) {
    const index = match.index || 0;
    if (index > cursor) fragment.append(document.createTextNode(safeText.slice(cursor, index)));
    const token = match[0];
    if (token.startsWith('![')) {
      const labelEnd = token.indexOf('](');
      const image = document.createElement('img');
      image.className = 'message-image';
      image.alt = token.slice(2, labelEnd) || 'image';
      image.src = token.slice(labelEnd + 2, -1);
      image.loading = 'lazy';
      image.addEventListener('click', () => showImagePreview(image.src, image.alt));
      fragment.append(image);
    } else if (token.startsWith('`')) {
      const code = document.createElement('code');
      code.textContent = token.slice(1, -1);
      fragment.append(code);
    } else if (token.startsWith('**')) {
      const strong = document.createElement('strong');
      strong.textContent = token.slice(2, -2);
      fragment.append(strong);
    } else if (token.startsWith('*')) {
      const em = document.createElement('em');
      em.textContent = token.slice(1, -1);
      fragment.append(em);
    } else {
      const labelEnd = token.indexOf('](');
      const href = token.slice(labelEnd + 2, -1);
      const link = document.createElement('a');
      link.textContent = token.slice(1, labelEnd);
      link.href = href;
      link.target = '_blank';
      link.rel = 'noreferrer';
      fragment.append(link);
    }
    cursor = index + token.length;
  }
  if (cursor < safeText.length) fragment.append(document.createTextNode(safeText.slice(cursor)));
  return fragment;
}

function renderMarkdown(text: string): DocumentFragment {
  const fragment = document.createDocumentFragment();
  const blocks = text.split(/\n{2,}/);
  blocks.forEach((block) => {
    const lines = block.split('\n');
    const isList = lines.every((line) => /^\s*[-*]\s+/.test(line));
    if (isList) {
      const ul = document.createElement('ul');
      lines.forEach((line) => {
        const li = document.createElement('li');
        li.append(parseInlineMarkdown(line.replace(/^\s*[-*]\s+/, '')));
        ul.append(li);
      });
      fragment.append(ul);
      return;
    }
    const paragraph = document.createElement('p');
    lines.forEach((line, lineIndex) => {
      if (lineIndex > 0) paragraph.append(document.createElement('br'));
      paragraph.append(parseInlineMarkdown(line));
    });
    fragment.append(paragraph);
  });
  return fragment;
}

function appendMessage(className: string, text: string, groupID = currentUserGroup, turnID = 0, attachments: PendingAttachment[] = []) {
  const list = document.getElementById('message-list');
  if (!list || !text) return;
  const item = document.createElement('div');
  item.className = `message ${className}`;
  item.dataset.seq = `${++messageSeq}`;
  item.dataset.group = `${groupID}`;
  if (turnID > 0) item.dataset.turnId = `${turnID}`;
  item.dataset.rawText = text;
  item.append(renderMarkdown(text));
  if (attachments.length) item.append(renderMessageAttachments(attachments));
  if (className.includes('message--user')) wireUserMessage(item, text);
  if (className.includes('message--error')) wireErrorMessage(item);
  list.appendChild(item);
  list.scrollTop = list.scrollHeight;
  return item;
}

function appendProgressBubble(label: 'waiting' | 'thinking' | 'working' | 'compacting' | 'stopping') {
  activeProgressBubble?.remove();
  const list = document.getElementById('message-list');
  if (!list) return null;
  const bubble = document.createElement('div');
  bubble.className = 'message message--status';
  bubble.dataset.status = label;
  bubble.innerHTML = `<span class="status-pulse" aria-hidden="true"><i></i><i></i><i></i></span><span>${label}</span>`;
  list.append(bubble);
  list.scrollTop = list.scrollHeight;
  activeProgressBubble = bubble;
  return bubble;
}

function clearProgressBubble() {
  activeProgressBubble?.remove();
  activeProgressBubble = null;
}

function showImagePreview(src: string, alt = 'image') {
  document.getElementById('image-preview')?.remove();
  const overlay = document.createElement('button');
  overlay.type = 'button';
  overlay.id = 'image-preview';
  overlay.className = 'image-preview';
  overlay.setAttribute('aria-label', 'Close image preview');
  const image = document.createElement('img');
  image.src = src;
  image.alt = alt;
  overlay.append(image);
  overlay.addEventListener('click', () => overlay.remove());
  document.body.append(overlay);
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
      : `<span class="attachment-file-icon">${attachmentKind(attachment)}</span>`;
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

function encodeAttachmentMeta(attachment: PendingAttachment) {
  const json = JSON.stringify({ name: attachment.name, type: attachment.type, size: attachment.size, path: attachment.path || '' });
  return btoa(unescape(encodeURIComponent(json)));
}

function decodeAttachmentMeta(encoded: string): PendingAttachment | null {
  try {
    return JSON.parse(decodeURIComponent(escape(atob(encoded)))) as PendingAttachment;
  } catch {
    return null;
  }
}

function parseTurnContent(content: string): { text: string; attachments: PendingAttachment[] } {
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
  return { text: text.trim(), attachments };
}

function clearMessages() {
  const list = document.getElementById('message-list');
  if (list) list.innerHTML = '';
  activeMessageMenu = null;
  messageSeq = 0;
  currentUserGroup = 0;
}

function getComposeInput() {
  return document.getElementById('compose-input') as HTMLTextAreaElement | null;
}

function setSubmittingUI(active: boolean) {
  const button = document.getElementById('send-btn') as HTMLButtonElement | null;
  if (!button) return;
  button.dataset.mode = active ? 'stop' : 'send';
  button.setAttribute('aria-label', active ? 'Stop response' : 'Kirim');
  button.title = active ? 'Stop response' : 'Kirim';
  const icon = button.querySelector('span');
  if (icon) icon.textContent = active ? '■' : '↗';
}

async function stopActiveResponse() {
  if (!submitting) return;
  appendProgressBubble('stopping');
  try {
    await StopChat(currentSessionID);
  } catch (err) {
    appendMessage('message--error', errorText(err), currentUserGroup);
  }
}

async function copyText(text: string) {
  if (!text) return;
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    const input = getComposeInput();
    if (!input) return;
    const previous = input.value;
    input.value = text;
    input.select();
    document.execCommand('copy');
    input.value = previous;
  }
}

function editText(text: string) {
  const input = getComposeInput();
  if (!input) return;
  input.value = text;
  input.focus();
  input.setSelectionRange(input.value.length, input.value.length);
  void refreshSlashSuggest();
}

function closeMessageMenu() {
  activeMessageMenu?.remove();
  activeMessageMenu = null;
}

async function deleteMessageBranch(turnID: number) {
  if (!turnID || submitting) return;
  closeMessageMenu();
  try {
    await DeleteChatTurn(currentSessionID, turnID);
    await restoreChatHistory();
  } catch (err) {
    appendMessage('message--error', errorText(err), currentUserGroup, turnID);
  }
}

// removeRepliesAfterTurn clears stale assistant/thinking/tool/error bubbles that
// belong to the retried user turn (and everything after it) so the regenerated
// response can stream into the same group instead of stacking on top of the old
// reply. Returns the group id of the retried user message so streamed events
// render in the correct place.
function removeRepliesAfterTurn(turnID: number): number {
  const user = document.querySelector<HTMLElement>(`.message--user[data-turn-id="${turnID}"]`);
  if (!user) return currentUserGroup;
  const group = Number(user.dataset.group || currentUserGroup);
  document.querySelectorAll<HTMLElement>('#message-list > .message').forEach((item) => {
    const itemGroup = Number(item.dataset.group || 0);
    if (itemGroup < group) return;
    if (item === user) return;
    if (itemGroup === group && item.classList.contains('message--user')) return;
    item.remove();
  });
  return group;
}

async function retryMessage(turnID: number) {
  const input = getComposeInput();
  if (!turnID || submitting || !input) return;
  closeMessageMenu();
  currentUserGroup = removeRepliesAfterTurn(turnID);
  setRingState('thinking');
  appendProgressBubble('waiting');
  const thinkingTimer = window.setTimeout(() => appendProgressBubble('thinking'), 450);
  submitting = true;
  setSubmittingUI(true);
  input.disabled = true;
  try {
    const res = await RetryChatTurn(currentSessionID, turnID);
    currentSessionID = res.session_id || currentSessionID;
    renderEvents((res.events || []) as StreamEvent[]);
    await bindLatestGroupTurnID();
    renderUsage(res.usage as ChatUsage | undefined);
  } catch (err) {
    appendMessage('message--error', errorText(err), currentUserGroup, turnID);
  } finally {
    window.clearTimeout(thinkingTimer);
    clearProgressBubble();
    submitting = false;
    setSubmittingUI(false);
    input.disabled = false;
    input.focus();
    setRingState('idle');
  }
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
      if (action === 'retry') void retryMessage(turnID);
      if (action === 'delete') void deleteMessageBranch(turnID);
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
    if (turnID) void retryMessage(turnID);
  });
  edit?.addEventListener('click', (event) => {
    event.stopPropagation();
    const groupID = item.dataset.group || '';
    const user = document.querySelector<HTMLElement>(`.message--user[data-group="${groupID}"]`);
    editText(user?.dataset.rawText || lastSubmittedText);
  });
  item.append(actions);
}

function renderTurn(turn: ChatTurn) {
  if (!turn.content) return;
  const parsed = parseTurnContent(turn.content);
  if (turn.role === 'user') {
    currentUserGroup++;
    appendMessage('message--user', parsed.text || parsed.attachments.map((item) => item.name).join(', '), currentUserGroup, turn.id, parsed.attachments);
  } else if (turn.role === 'error') appendMessage('message--error', turn.content, currentUserGroup, turn.id);
  else appendMessage('message--assistant', turn.content, currentUserGroup, turn.id);
}

async function restoreChatHistory() {
  try {
    const history = await ChatHistory();
    currentSessionID = history.session_id || currentSessionID;
    clearMessages();
    (history.turns || []).forEach((turn: ChatTurn) => renderTurn(turn));
    renderUsage(history.usage as ChatUsage | undefined);
  } catch {
    // Core may not be ready yet; ping loop will update connection state.
  }
}

async function bindLatestGroupTurnID() {
  try {
    const history = await ChatHistory();
    const turns = (history.turns || []) as ChatTurn[];
    const user = [...turns].reverse().find((turn) => turn.role === 'user');
    if (!user) return 0;
    document.querySelectorAll<HTMLElement>(`.message[data-group="${currentUserGroup}"]`).forEach((item) => {
      item.dataset.turnId = `${user.id}`;
    });
    return user.id;
  } catch {
    return 0;
  }
}

function errorText(err: unknown) {
  if (err instanceof Error && err.message) return err.message;
  if (typeof err === 'string') return err;
  return 'unknown error';
}

// Streaming coalescer: accumulates delta chunks into one bubble so
// word-by-word streams (e.g. blackbox MiniMax-M3) render as natural typing
// instead of spawning a new DOM node per token.
type StreamTarget = { el: HTMLElement; text: string; queue: string; typing: boolean };

function makeStreamTarget(className: string): StreamTarget {
  const list = document.getElementById('message-list');
  const el = document.createElement('div');
  el.className = `message ${className} message--streaming`;
  el.dataset.seq = `${++messageSeq}`;
  el.dataset.group = `${currentUserGroup}`;
  if (list) list.appendChild(el);
  list && (list.scrollTop = list.scrollHeight);
  return { el, text: '', queue: '', typing: false };
}

function paintStream(target: StreamTarget) {
  target.el.replaceChildren(renderMarkdown(target.text));
  const list = document.getElementById('message-list');
  if (list) list.scrollTop = list.scrollHeight;
}

function typeNext(target: StreamTarget) {
  if (!target.queue) {
    target.typing = false;
    return;
  }
  const step = Math.max(1, Math.min(3, Math.ceil(target.queue.length / 90)));
  target.text += target.queue.slice(0, step);
  target.queue = target.queue.slice(step);
  paintStream(target);
  window.setTimeout(() => typeNext(target), target.queue.length > 240 ? 8 : 18);
}

function flushStream(target: StreamTarget) {
  if (target.queue) {
    target.text += target.queue;
    target.queue = '';
    paintStream(target);
  }
  target.el.dataset.rawText = target.text;
  target.typing = false;
  target.el.classList.remove('message--streaming');
}

function pushStream(target: StreamTarget, chunk: string) {
  if (!chunk) return;
  target.queue += chunk;
  if (!target.typing) {
    target.typing = true;
    typeNext(target);
  }
}

function renderEvents(events: StreamEvent[]) {
  let thinking: StreamTarget | null = null;
  let assistant: StreamTarget | null = null;
  for (const event of events) {
    if (event.kind === 'thinking_delta') {
      setRingState('thinking');
      if (!thinking) thinking = makeStreamTarget('message--thinking');
      pushStream(thinking, event.delta || '');
    } else if (event.kind === 'tool_call') {
      setRingState('delegating');
      // Flush any pending stream before tool call so it shows up cleanly.
      if (thinking) { flushStream(thinking); thinking = null; }
      if (assistant) { flushStream(assistant); assistant = null; }
      appendMessage('message--tool', `tool: ${event.tool_call?.name || 'unknown'}`);
    } else if (event.kind === 'response_delta') {
      clearProgressBubble();
      if (!assistant) assistant = makeStreamTarget('message--assistant');
      pushStream(assistant, event.delta || '');
    } else if (event.kind === 'status') {
      const status = event.status;
      if (status === 'waiting' || status === 'thinking' || status === 'working' || status === 'compacting' || status === 'stopping') {
        appendProgressBubble(status);
      }
    } else if (event.kind === 'error') {
      clearProgressBubble();
      if (thinking) { flushStream(thinking); thinking = null; }
      if (assistant) { flushStream(assistant); assistant = null; }
      appendMessage('message--error', event.error || 'chat failed');
      setRingState('idle');
    } else if (event.kind === 'done') {
      clearProgressBubble();
      if (thinking) { flushStream(thinking); thinking = null; }
      if (assistant) { flushStream(assistant); assistant = null; }
      setRingState('idle');
    }
  }
  // Drain any remaining buffered text.
  if (thinking) flushStream(thinking);
  if (assistant) flushStream(assistant);
}

async function runPing() {
  try {
    const res = await PingCore();
    lastLatencyMs = res.round_trip_ms;
    setConnection('connected');
    if (res.ring_state) setRingState(res.ring_state as RingState);
    renderRingBadge();
  } catch {
    lastLatencyMs = -1;
    setConnection(connection === 'connected' ? 'reconnecting' : 'disconnected');
    renderRingBadge();
  }
}

function startPingLoop() {
  if (pingTimer) return;
  void runPing();
  pingTimer = setInterval(() => void runPing(), 4000);
}

function fileToDataURI(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ''));
    reader.onerror = () => reject(reader.error || new Error('failed to read file'));
    reader.readAsDataURL(file);
  });
}

function fileToText(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ''));
    reader.onerror = () => reject(reader.error || new Error('failed to read file'));
    reader.readAsText(file);
  });
}

function formatBytes(bytes: number) {
  if (!bytes) return '';
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

function attachmentKind(attachment: PendingAttachment) {
  if (attachment.type.startsWith('image/')) return 'IMG';
  return 'FILE';
}

function renderAttachments() {
  const tray = document.getElementById('attachment-tray');
  const input = document.getElementById('compose-input') as HTMLTextAreaElement | null;
  const wrap = document.getElementById('compose-wrap');
  if (!tray) return;
  tray.innerHTML = '';
  tray.dataset.count = `${pendingAttachments.length}`;
  if (pendingAttachments.length) {
    input?.classList.add('has-attachments');
    wrap?.classList.add('has-attachments');
  } else {
    input?.classList.remove('has-attachments');
    wrap?.classList.remove('has-attachments');
  }
  pendingAttachments.forEach((attachment, index) => {
    const chip = document.createElement('button');
    chip.type = 'button';
    chip.className = 'attachment-chip';
    chip.title = 'Klik untuk hapus attachment';
    chip.innerHTML = `<span class="attachment-kind">${attachmentKind(attachment)}</span><span class="attachment-name"></span><span class="attachment-size">${formatBytes(attachment.size)}</span><span class="attachment-remove">×</span>`;
    const name = chip.querySelector('.attachment-name');
    if (name) name.textContent = attachment.name;
    chip.addEventListener('click', () => {
      pendingAttachments.splice(index, 1);
      renderAttachments();
    });
    tray.append(chip);
  });
}

function buildAttachmentPrompt() {
  return pendingAttachments.map((attachment) => {
    const metadata = `<!--sapaloq-attachment:${encodeAttachmentMeta(attachment)}-->`;
    if (attachment.dataURI) return `${metadata}\n![${attachment.name}](${attachment.dataURI})`;
    return `${metadata}\n--- file: ${attachment.name} (${attachment.type || 'text/plain'}) ---\n${attachment.text || ''}\n--- end file: ${attachment.name} ---`;
  }).join('\n');
}

async function addFiles(files: FileList | File[]) {
  const incoming = Array.from(files).filter(Boolean);
  if (!incoming.length) return;
  const tray = document.getElementById('attachment-tray');
  const wrap = document.getElementById('compose-wrap');
  tray?.classList.add('is-loading');
  wrap?.classList.add('has-attachments');
  try {
    for (const file of incoming) {
      if (file.type.startsWith('image/')) {
        pendingAttachments.push({ name: file.name || 'pasted-image', type: file.type, size: file.size, dataURI: await fileToDataURI(file) });
      } else {
        pendingAttachments.push({ name: file.name || 'pasted-file', type: file.type || 'text/plain', size: file.size, text: await fileToText(file) });
      }
    }
  } finally {
    tray?.classList.remove('is-loading');
    renderAttachments();
    document.getElementById('compose-input')?.focus();
  }
}

async function addClipboardItems(clipboard: DataTransfer | null) {
  if (!clipboard) return false;
  const files = collectTransferFiles(clipboard);
  if (!files.length) return false;
  await addFiles(files);
  return true;
}

// Ingest native (Wails OnFileDrop) file paths. WebKitGTK cannot hand File
// objects to the webview for out-of-browser drops, so the drag is handled in
// GTK and we get paths back. The webview cannot read file:// URLs itself, so
// each path is read Go-side via ReadDroppedFile and turned into the same
// PendingAttachment shape as paste/browser drops.
async function addDroppedPaths(paths: string[]) {
  const incoming = paths.map((p) => p.trim()).filter(Boolean);
  if (!incoming.length) return;
  const tray = document.getElementById('attachment-tray');
  const wrap = document.getElementById('compose-wrap');
  tray?.classList.add('is-loading');
  wrap?.classList.add('has-attachments');
  try {
    for (const path of incoming) {
      try {
        const file = await ReadDroppedFile(path);
        if (!file) continue;
        pendingAttachments.push({
          name: file.name,
          path: file.path || undefined,
          type: file.mime || (file.is_image ? 'image/*' : 'text/plain'),
          size: file.size,
          dataURI: file.data_uri || undefined,
          text: file.text || undefined,
        });
      } catch (err) {
        appendMessage('message--error', `gagal membaca ${path.split('/').pop()}: ${String(err)}`);
      }
    }
  } finally {
    tray?.classList.remove('is-loading');
    renderAttachments();
    document.getElementById('compose-input')?.focus();
  }
}

function dataURIToFile(dataURI: string, fallbackName = 'dropped-image'): File | null {
  const match = /^data:([^;,]+)?(;base64)?,(.*)$/.exec(dataURI.trim());
  if (!match) return null;
  const mime = match[1] || 'application/octet-stream';
  const isBase64 = Boolean(match[2]);
  const payload = match[3];
  let bytes: Uint8Array;
  try {
    const bin = isBase64 ? atob(payload) : decodeURIComponent(payload);
    bytes = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  } catch {
    return null;
  }
  const ext = (mime.split('/')[1] || 'bin').split(';')[0];
  return new File([bytes], `${fallbackName}.${ext}`, { type: mime });
}

// Collect File objects from a drop DataTransfer. Some sources (file managers on
// WebKitGTK, in-window image drags) populate `items` but leave `files` empty, so
// we must read both. getAsFile() only works while the drop event is live, hence
// this stays synchronous. As a last resort, rendered-image drags expose only a
// URL string — convert data:image/... URIs to a File.
function collectTransferFiles(transfer: DataTransfer | null): File[] {
  if (!transfer) return [];
  const files: File[] = [];
  const seen = new Set<string>();
  const push = (file: File | null | undefined) => {
    if (!file) return;
    const key = `${file.name}|${file.size}|${file.type}`;
    if (seen.has(key)) return;
    seen.add(key);
    files.push(file);
  };
  for (const file of Array.from(transfer.files || [])) push(file);
  for (const item of Array.from(transfer.items || [])) {
    if (item.kind === 'file') push(item.getAsFile());
  }
  if (!files.length) {
    const uriList = transfer.getData('text/uri-list') || transfer.getData('text/plain') || '';
    for (const line of uriList.split(/\r?\n/)) {
      const uri = line.trim();
      if (uri.startsWith('data:image/')) push(dataURIToFile(uri));
    }
  }
  return files;
}

async function sendText(text: string, visibleText = text, attachments: PendingAttachment[] = []) {
  const input = getComposeInput();
  if (submitting || !input || !text.trim()) return;
  closeMessageMenu();
  hideSlashSuggest();
  lastSubmittedText = text;
  currentUserGroup++;
  appendMessage('message--user', visibleText.trim(), currentUserGroup, 0, attachments);
  setRingState('thinking');
  appendProgressBubble('waiting');
  const thinkingTimer = window.setTimeout(() => appendProgressBubble('thinking'), 450);
  submitting = true;
  setSubmittingUI(true);
  input.disabled = true;
  try {
    const res = await SendMessage(currentSessionID, text);
    currentSessionID = res.session_id || currentSessionID;
    renderEvents((res.events || []) as StreamEvent[]);
    await bindLatestGroupTurnID();
    renderUsage(res.usage as ChatUsage | undefined);
    if (text.trim() === '/reset') {
      await restoreChatHistory();
    }
    void runPing();
  } catch (err) {
    const message = errorText(err);
    appendMessage('message--error', message.includes('dial ') ? 'core offline' : message, currentUserGroup);
    setConnection(message.includes('dial ') ? 'disconnected' : 'connected');
    setRingState('idle');
  } finally {
    window.clearTimeout(thinkingTimer);
    clearProgressBubble();
    submitting = false;
    setSubmittingUI(false);
    input.disabled = false;
    input.focus();
  }
}

async function submitMessage() {
  const input = getComposeInput();
  const attachmentPrompt = buildAttachmentPrompt();
  if (!input || (!input.value.trim() && !attachmentPrompt)) return;
  const text = `${input.value.trim()}${attachmentPrompt}`.trim();
  const visibleText = input.value.trim() || pendingAttachments.map((file) => file.name).join(', ');
  const sentAttachments = pendingAttachments.map((attachment) => ({ ...attachment }));
  input.value = '';
  pendingAttachments = [];
  renderAttachments();
  await sendText(text, visibleText, sentAttachments);
}

function hideSlashSuggest() {
  const popover = document.getElementById('slash-popover');
  if (popover) popover.innerHTML = '';
}

async function refreshSlashSuggest() {
  const input = document.getElementById('compose-input') as HTMLTextAreaElement | null;
  const popover = document.getElementById('slash-popover');
  if (!input || !popover) return;
  const active = activeSlashAtChat(input.value, input.selectionStart || 0);
  if (!active) {
    hideSlashSuggest();
    return;
  }
  try {
    const suggestions = (await SlashSuggest(active.query)) as CommandEntry[];
    popover.innerHTML = suggestions
      .filter((entry) => entry.enabled !== false)
      .map((entry) => `<button type="button" class="slash-item" data-prefix="${entry.prefix}"><strong>${entry.label}</strong><span>${entry.description}</span></button>`)
      .join('');
    popover.querySelectorAll<HTMLButtonElement>('.slash-item').forEach((button) => {
      button.addEventListener('click', () => {
        const prefix = button.dataset.prefix || '';
        input.value = input.value.slice(0, active.slashIndex) + prefix + input.value.slice(input.selectionStart || 0);
        input.focus();
        input.setSelectionRange(active.slashIndex + prefix.length, active.slashIndex + prefix.length);
        hideSlashSuggest();
      });
    });
  } catch {
    hideSlashSuggest();
  }
}

const SLASH_BOUNDARY = /(^\/|\s\/|\n\/)/;

document.querySelector('#app')!.innerHTML = `
  <div class="dock">
    <section class="popup" id="popup" aria-hidden="true" style="--wails-draggable: no-drag">
      <header class="popup-header">
        <div class="popup-brand">
          <span class="brand-mark" aria-hidden="true"><span class="brand-mark-core"></span></span>
          <span class="brand-copy"><span class="popup-name">SapaLOQ</span></span>
        </div>
        <div class="popup-header-right">
          <span class="context-usage" id="context-usage" data-level="normal" title="context usage">0/0</span>
          <span class="conn-pill"><span class="conn-dot" id="conn-dot" data-state="connecting" aria-label="status koneksi" title="menghubungkan…"></span><span>core</span></span>
          <button type="button" class="popup-resize" id="btn-resize" aria-label="Ubah ukuran chat" title="Ubah ukuran chat">□</button>
          <button type="button" class="popup-close" id="btn-close" aria-label="Tutup">×</button>
        </div>
      </header>
      <div class="popup-body">
        <div class="empty-state" aria-hidden="true">
          <span class="empty-kicker">ready</span>
          <strong>Ask, route, delegate.</strong>
          <span>Gunakan <code>/</code> untuk command cepat.</span>
        </div>
        <div class="message-list" id="message-list"></div>
      </div>
      <footer class="popup-compose">
        <div class="compose-wrap" id="compose-wrap">
          <div class="slash-popover" id="slash-popover"></div>
          <div class="attachment-tray" id="attachment-tray" aria-live="polite"></div>
          <textarea id="compose-input" placeholder="Ask anything" autocomplete="off" rows="1"></textarea>
          <input type="file" id="attach-input" class="attach-input" multiple aria-hidden="true" tabindex="-1">
        </div>
        <button type="button" class="attach-btn" id="attach-btn" aria-label="Attach file" title="Attach file"><span>＋</span></button>
        <button type="button" class="send-btn" id="send-btn" aria-label="Kirim"><span>↗</span></button>
      </footer>
    </section>
    <div class="fab-row"><button type="button" class="orb" id="orb" data-state="idle" aria-label="Buka SapaLOQ" style="--wails-draggable: drag"><span class="orb-aura" aria-hidden="true"></span><span class="orb-ring" aria-hidden="true"></span><span class="orb-body" aria-hidden="true"><span class="orb-grid" aria-hidden="true"></span><span class="sapa-glyph" aria-hidden="true"><span class="glyph-node glyph-node--a"></span><span class="glyph-node glyph-node--b"></span><span class="glyph-node glyph-node--c"></span><span class="glyph-path glyph-path--a"></span><span class="glyph-path glyph-path--b"></span></span><span class="orb-specular" aria-hidden="true"></span><span class="ring-badge" id="ring-badge" aria-hidden="true"></span><span class="orb-chevron" aria-hidden="true">⌄</span></span></button></div>
  </div>
`;

void initWindowLayout();

let clickTimer: ReturnType<typeof setTimeout> | null = null;
document.getElementById('orb')?.addEventListener('click', (e) => {
  e.stopPropagation();
  if (e.altKey) {
    void runPing();
    return;
  }
  if (clickTimer) return;
  clickTimer = setTimeout(() => {
    clickTimer = null;
    void toggleExpanded();
  }, 200);
});
document.getElementById('btn-close')?.addEventListener('click', () => void setExpanded(false));
document.getElementById('btn-resize')?.addEventListener('click', () => void cyclePanelSize());
document.getElementById('orb')?.addEventListener('dblclick', (e) => {
  e.preventDefault();
  if (clickTimer) {
    clearTimeout(clickTimer);
    clickTimer = null;
  }
  if (!isExpanded()) cycleRingState();
});
document.getElementById('send-btn')?.addEventListener('click', () => {
  if (submitting) void stopActiveResponse();
  else void submitMessage();
});
document.getElementById('attach-btn')?.addEventListener('click', () => {
  const input = document.getElementById('attach-input') as HTMLInputElement | null;
  input?.click();
});
document.getElementById('attach-input')?.addEventListener('change', (event) => {
  const input = event.currentTarget as HTMLInputElement;
  if (input.files?.length) void addFiles(input.files);
  input.value = '';
});
document.getElementById('compose-input')?.addEventListener('input', () => void refreshSlashSuggest());
document.getElementById('compose-input')?.addEventListener('keyup', () => void refreshSlashSuggest());
document.getElementById('compose-input')?.addEventListener('keydown', (event) => {
  const keyEvent = event as KeyboardEvent;
  if (keyEvent.key === 'Enter' && !keyEvent.shiftKey) {
    event.preventDefault();
    void submitMessage();
  }
});
document.getElementById('compose-input')?.addEventListener('paste', (event) => {
  const clipboard = (event as ClipboardEvent).clipboardData;
  if ((clipboard?.files?.length || clipboard?.items?.length) && Array.from(clipboard.items || []).some((item) => item.kind === 'file')) {
    event.preventDefault();
  }
  void addClipboardItems(clipboard);
});
document.addEventListener('click', (event) => {
  const target = event.target as HTMLElement | null;
  if (!target?.closest('.message-menu') && !target?.closest('.message--user')) closeMessageMenu();
});

document.addEventListener('paste', (event) => {
  if (document.activeElement?.id === 'compose-input') return;
  const clipboard = (event as ClipboardEvent).clipboardData;
  if (Array.from(clipboard?.items || []).some((item) => item.kind === 'file')) event.preventDefault();
  void addClipboardItems(clipboard).then((handled) => {
    if (handled) {
      void setExpanded(true);
      document.getElementById('compose-input')?.focus();
    }
  });
});
const popup = document.getElementById('popup');

// Highlight helpers shared by native (OnFileDrop) and HTML drag paths.
let dragDepth = 0;
function showDragOverlay() {
  dragDepth++;
  popup?.classList.add('is-dragging-file');
}
function hideDragOverlay(force = false) {
  if (force) dragDepth = 0;
  else dragDepth = Math.max(0, dragDepth - 1);
  if (dragDepth === 0) popup?.classList.remove('is-dragging-file');
}

// Native file drop (Wails). On WebKitGTK the webview drag events are disabled
// (DisableWebViewDrop:true in main.go), so the only way to receive drops from
// the file manager / desktop is this GTK-level callback, which hands us file
// *paths*. Listen on the whole native window: target-scoped drops become
// unreliable after the GTK input shape switches between orb and panel.
try {
  EventsOn('sapaloq:status', (event: StreamEvent) => {
    const status = event?.status;
    if (status === 'waiting' || status === 'thinking' || status === 'working' || status === 'compacting' || status === 'stopping') {
      appendProgressBubble(status);
    }
  });
  OnFileDrop((_x, _y, paths) => {
    if (paths?.length) {
      hideDragOverlay(true);
      if (!isExpanded()) void setExpanded(true);
      void addDroppedPaths(paths);
      document.getElementById('compose-input')?.focus();
    }
  }, false);
} catch {
  // OnFileDrop only exists inside a Wails runtime; ignore in plain browser.
}

// HTML drag fallback for environments where the webview *does* deliver File
// objects (Chromium, browser preview, in-webview image drags). WebKitGTK with
// DisableWebViewDrop:true will never reach these, so there is no conflict.
popup?.addEventListener('dragenter', (event) => {
  event.preventDefault();
  showDragOverlay();
});
popup?.addEventListener('dragover', (event) => {
  event.preventDefault();
  if (!popup.classList.contains('is-dragging-file')) showDragOverlay();
});
popup?.addEventListener('dragleave', (event) => {
  // Only count leaves that actually exit the popup rect, not child crossings.
  const r = popup.getBoundingClientRect();
  if (event.clientX <= r.left || event.clientX >= r.right || event.clientY <= r.top || event.clientY >= r.bottom) {
    hideDragOverlay();
  }
});
popup?.addEventListener('drop', (event) => {
  event.preventDefault();
  hideDragOverlay(true);
  const transfer = (event as DragEvent).dataTransfer;
  const files = collectTransferFiles(transfer);
  if (files.length) void addFiles(files);
});
// Document-level fallback so the overlay still shows when the popup is
// collapsed (pointer-events:none on #popup blocks its own dragover).
document.addEventListener('dragover', (event) => {
  if (popup?.classList.contains('is-dragging-file')) return;
  if (document.getElementById('popup')) {
    event.preventDefault();
    showDragOverlay();
  }
});
document.addEventListener('drop', (event) => {
  if (!popup?.classList.contains('is-dragging-file')) return;
  event.preventDefault();
  hideDragOverlay(true);
  const transfer = (event as DragEvent).dataTransfer;
  const files = collectTransferFiles(transfer);
  if (files.length) void addFiles(files);
});

void restoreChatHistory();
void ContextUsage().then((usage) => renderUsage(usage as ChatUsage)).catch(() => undefined);
startPingLoop();
