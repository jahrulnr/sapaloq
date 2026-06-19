import './style.css';
import { ChatHistory, ContextUsage, PingCore, SendMessage, SlashSuggest } from '../wailsjs/go/main/App';
import { initWindowLayout, isExpanded, setExpanded, toggleExpanded } from './window-layout';

type RingState = 'idle' | 'thinking' | 'delegating' | 'needs-input';
type ConnectionState = 'connecting' | 'connected' | 'reconnecting' | 'disconnected';
type CommandEntry = { id: string; prefix: string; label: string; description: string; enabled: boolean };
type StreamEvent = { kind: string; delta?: string; error?: string; tool_call?: { name: string } };
type ChatTurn = { role: string; content: string };
type ChatUsage = { session_id: string; used_tokens: number; context_window: number; percent: number; provider: string; model: string };
type PendingAttachment = { name: string; type: string; size: number; dataURI?: string; text?: string };

const states: RingState[] = ['idle', 'thinking', 'delegating', 'needs-input'];
let stateIndex = 0;
let lastLatencyMs = -1;
let connection: ConnectionState = 'connecting';
let pingTimer: ReturnType<typeof setInterval> | null = null;
let submitting = false;
let currentSessionID = '';
let pendingAttachments: PendingAttachment[] = [];

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

function parseInlineMarkdown(text: string): DocumentFragment {
  const fragment = document.createDocumentFragment();
  const pattern = /(`[^`]+`|\*\*[^*]+\*\*|\*[^*]+\*|\[[^\]]+\]\((https?:\/\/[^\s)]+)\))/g;
  let cursor = 0;
  for (const match of text.matchAll(pattern)) {
    const index = match.index || 0;
    if (index > cursor) fragment.append(document.createTextNode(text.slice(cursor, index)));
    const token = match[0];
    if (token.startsWith('`')) {
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
  if (cursor < text.length) fragment.append(document.createTextNode(text.slice(cursor)));
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

function appendMessage(className: string, text: string) {
  const list = document.getElementById('message-list');
  if (!list || !text) return;
  const item = document.createElement('div');
  item.className = `message ${className}`;
  item.append(renderMarkdown(text));
  list.appendChild(item);
  list.scrollTop = list.scrollHeight;
  return item;
}

function clearMessages() {
  const list = document.getElementById('message-list');
  if (list) list.innerHTML = '';
}

function renderTurn(turn: ChatTurn) {
  if (!turn.content) return;
  if (turn.role === 'user') appendMessage('message--user', turn.content);
  else if (turn.role === 'error') appendMessage('message--error', turn.content);
  else appendMessage('message--assistant', turn.content);
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

function errorText(err: unknown) {
  if (err instanceof Error && err.message) return err.message;
  if (typeof err === 'string') return err;
  return 'unknown error';
}

// Streaming coalescer: accumulates delta chunks into one bubble so
// word-by-word streams (e.g. blackbox MiniMax-M3) render as natural typing
// instead of spawning a new DOM node per token.
type StreamTarget = { el: HTMLElement; buffer: string; scheduled: boolean };

function makeStreamTarget(className: string): StreamTarget {
  const list = document.getElementById('message-list');
  const el = document.createElement('div');
  el.className = `message ${className}`;
  if (list) list.appendChild(el);
  list && (list.scrollTop = list.scrollHeight);
  return { el, buffer: '', scheduled: false };
}

function flushStream(target: StreamTarget) {
  target.scheduled = false;
  if (!target.buffer) return;
  target.el.textContent += target.buffer;
  target.buffer = '';
  const list = document.getElementById('message-list');
  if (list) list.scrollTop = list.scrollHeight;
}

function pushStream(target: StreamTarget, chunk: string, flushBoundary = false) {
  if (!chunk) return;
  target.buffer += chunk;
  // Flush immediately on whitespace boundary (natural word boundary) so
  // each visible token corresponds to one render.
  if (flushBoundary || /\s/.test(chunk)) {
    flushStream(target);
    return;
  }
  if (!target.scheduled) {
    target.scheduled = true;
    requestAnimationFrame(() => flushStream(target));
  }
}

function renderEvents(events: StreamEvent[]) {
  let thinking: StreamTarget | null = null;
  let assistant: StreamTarget | null = null;
  for (const event of events) {
    if (event.kind === 'thinking_delta') {
      setRingState('thinking');
      if (!thinking) thinking = makeStreamTarget('message--thinking');
      pushStream(thinking, event.delta || '', true);
    } else if (event.kind === 'tool_call') {
      setRingState('delegating');
      // Flush any pending stream before tool call so it shows up cleanly.
      if (thinking) { flushStream(thinking); thinking = null; }
      if (assistant) { flushStream(assistant); assistant = null; }
      appendMessage('message--tool', `tool: ${event.tool_call?.name || 'unknown'}`);
    } else if (event.kind === 'response_delta') {
      if (!assistant) assistant = makeStreamTarget('message--assistant');
      pushStream(assistant, event.delta || '');
    } else if (event.kind === 'error') {
      if (thinking) { flushStream(thinking); thinking = null; }
      if (assistant) { flushStream(assistant); assistant = null; }
      appendMessage('message--error', event.error || 'chat failed');
      setRingState('idle');
    } else if (event.kind === 'done') {
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

function renderAttachments() {
  const tray = document.getElementById('attachment-tray');
  if (!tray) return;
  tray.innerHTML = '';
  pendingAttachments.forEach((attachment, index) => {
    const chip = document.createElement('button');
    chip.type = 'button';
    chip.className = 'attachment-chip';
    chip.title = 'Klik untuk hapus attachment';
    chip.textContent = `${attachment.type.startsWith('image/') ? 'image' : 'file'} · ${attachment.name}`;
    chip.addEventListener('click', () => {
      pendingAttachments.splice(index, 1);
      renderAttachments();
    });
    tray.append(chip);
  });
}

function buildAttachmentPrompt() {
  return pendingAttachments.map((attachment) => {
    if (attachment.dataURI) return `![${attachment.name}](${attachment.dataURI})`;
    return `\n\n--- file: ${attachment.name} (${attachment.type || 'text/plain'}) ---\n${attachment.text || ''}\n--- end file: ${attachment.name} ---`;
  }).join('\n');
}

async function addFiles(files: FileList | File[]) {
  const incoming = Array.from(files).filter(Boolean);
  if (!incoming.length) return;
  for (const file of incoming) {
    if (file.type.startsWith('image/')) {
      pendingAttachments.push({ name: file.name || 'pasted-image', type: file.type, size: file.size, dataURI: await fileToDataURI(file) });
    } else {
      pendingAttachments.push({ name: file.name || 'pasted-file', type: file.type || 'text/plain', size: file.size, text: await fileToText(file) });
    }
  }
  renderAttachments();
}

async function submitMessage() {
  if (submitting) return;
  const input = document.getElementById('compose-input') as HTMLTextAreaElement | null;
  const attachmentPrompt = buildAttachmentPrompt();
  if (!input || (!input.value.trim() && !attachmentPrompt)) return;
  const text = `${input.value.trim()}${attachmentPrompt}`.trim();
  const visibleText = input.value.trim() || pendingAttachments.map((file) => file.name).join(', ');
  input.value = '';
  pendingAttachments = [];
  renderAttachments();
  hideSlashSuggest();
  appendMessage('message--user', visibleText);
  setRingState('thinking');
  submitting = true;
  input.disabled = true;
  try {
    const res = await SendMessage(currentSessionID, text);
    currentSessionID = res.session_id || currentSessionID;
    renderEvents((res.events || []) as StreamEvent[]);
    renderUsage(res.usage as ChatUsage | undefined);
    if (text.trim() === '/reset') {
      await restoreChatHistory();
    }
    void runPing();
  } catch (err) {
    const message = errorText(err);
    appendMessage('message--error', message.includes('dial ') ? 'core offline' : message);
    setConnection(message.includes('dial ') ? 'disconnected' : 'connected');
    setRingState('idle');
  } finally {
    submitting = false;
    input.disabled = false;
    input.focus();
  }
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
          <span class="brand-copy"><span class="popup-name">SapaLOQ</span><span class="popup-tagline">local orbit queue</span></span>
        </div>
        <div class="popup-header-right">
          <span class="context-usage" id="context-usage" data-level="normal" title="context usage">0/0</span>
          <span class="conn-pill"><span class="conn-dot" id="conn-dot" data-state="connecting" aria-label="status koneksi" title="menghubungkan…"></span><span>core</span></span>
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
        <div class="compose-wrap">
          <div class="slash-popover" id="slash-popover"></div>
          <div class="attachment-tray" id="attachment-tray" aria-live="polite"></div>
          <textarea id="compose-input" placeholder="Tulis instruksi, paste gambar, atau drag file…" autocomplete="off" rows="1"></textarea>
        </div>
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
document.getElementById('orb')?.addEventListener('dblclick', (e) => {
  e.preventDefault();
  if (clickTimer) {
    clearTimeout(clickTimer);
    clickTimer = null;
  }
  if (!isExpanded()) cycleRingState();
});
document.getElementById('send-btn')?.addEventListener('click', () => void submitMessage());
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
  if (clipboard?.files?.length) void addFiles(clipboard.files);
});
const popup = document.getElementById('popup');
popup?.addEventListener('dragover', (event) => {
  event.preventDefault();
  popup.classList.add('is-dragging-file');
});
popup?.addEventListener('dragleave', () => popup.classList.remove('is-dragging-file'));
popup?.addEventListener('drop', (event) => {
  event.preventDefault();
  popup.classList.remove('is-dragging-file');
  const files = (event as DragEvent).dataTransfer?.files;
  if (files?.length) void addFiles(files);
});

void restoreChatHistory();
void ContextUsage().then((usage) => renderUsage(usage as ChatUsage)).catch(() => undefined);
startPingLoop();
