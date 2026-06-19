import './style.css';
import { PingCore, SendMessage, SlashSuggest } from '../wailsjs/go/main/App';
import { initWindowLayout, isExpanded, setExpanded, toggleExpanded } from './window-layout';

type RingState = 'idle' | 'thinking' | 'delegating' | 'needs-input';
type ConnectionState = 'connecting' | 'connected' | 'reconnecting' | 'disconnected';
type CommandEntry = { id: string; prefix: string; label: string; description: string; enabled: boolean };
type StreamEvent = { kind: string; delta?: string; error?: string; tool_call?: { name: string } };

const states: RingState[] = ['idle', 'thinking', 'delegating', 'needs-input'];
let stateIndex = 0;
let lastLatencyMs = -1;
let connection: ConnectionState = 'connecting';
let pingTimer: ReturnType<typeof setInterval> | null = null;
let submitting = false;

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

function cycleRingState() {
  stateIndex = (stateIndex + 1) % states.length;
  setRingState(states[stateIndex]);
}

function appendMessage(className: string, text: string) {
  const list = document.getElementById('message-list');
  if (!list || !text) return;
  const item = document.createElement('div');
  item.className = `message ${className}`;
  item.textContent = text;
  list.appendChild(item);
  list.scrollTop = list.scrollHeight;
  return item;
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

async function submitMessage() {
  if (submitting) return;
  const input = document.getElementById('compose-input') as HTMLInputElement | null;
  if (!input || !input.value.trim()) return;
  const text = input.value.trim();
  input.value = '';
  hideSlashSuggest();
  appendMessage('message--user', text);
  setRingState('thinking');
  submitting = true;
  input.disabled = true;
  try {
    const res = await SendMessage(text);
    renderEvents((res.events || []) as StreamEvent[]);
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
  const input = document.getElementById('compose-input') as HTMLInputElement | null;
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
          <input id="compose-input" type="text" placeholder="Tulis instruksi atau /command…" autocomplete="off" />
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
  if ((event as KeyboardEvent).key === 'Enter') {
    event.preventDefault();
    void submitMessage();
  }
});

startPingLoop();
