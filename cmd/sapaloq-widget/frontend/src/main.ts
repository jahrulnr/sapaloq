import './style.css';
import { PingCore, SendMessage, SlashSuggest, SocketPath } from '../wailsjs/go/main/App';
import { initWindowLayout, isExpanded, setExpanded, toggleExpanded } from './window-layout';

const iconWave = `<svg class="icon-svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M7 11V6a2 2 0 0 1 4 0v1"/><path d="M11 10V5a2 2 0 0 1 4 0v2"/><path d="M15 9V7a2 2 0 0 1 4 0v8a4 4 0 0 1-4 4H9a5 5 0 0 1-5-5v-3a2 2 0 0 1 4 0"/></svg>`;
const iconChat = `<svg class="icon-svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 11.5a8.4 8.4 0 0 1-1.9 5.4 8.5 8.5 0 0 1-6.6 3.1 8.4 8.4 0 0 1-4.2-1.1L3 21v-4.6a8.4 8.4 0 0 1-1.2-4.3 8.5 8.5 0 0 1 3.1-6.6A8.4 8.4 0 0 1 11.5 3 8.5 8.5 0 0 1 21 11.5z"/></svg>`;

const SLASH_BOUNDARY = /(^\/|\s\/|\n\/)/;

type RingState = 'idle' | 'thinking' | 'delegating' | 'needs-input';
type CommandEntry = { id: string; prefix: string; label: string; description: string; enabled: boolean };
type StreamEvent = { kind: string; delta?: string; tool_call?: { name: string } };

const states: RingState[] = ['idle', 'thinking', 'delegating', 'needs-input'];
let stateIndex = 0;

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
  const badge = document.getElementById('status-badge');
  if (badge) badge.textContent = next;
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
}

function renderEvents(events: StreamEvent[]) {
  for (const event of events) {
    if (event.kind === 'thinking_delta') {
      setRingState('thinking');
      appendMessage('message--thinking', event.delta || 'thinking…');
    } else if (event.kind === 'tool_call') {
      setRingState('delegating');
      appendMessage('message--tool', `tool: ${event.tool_call?.name || 'unknown'}`);
    } else if (event.kind === 'response_delta') {
      appendMessage('message--assistant', event.delta || '');
    } else if (event.kind === 'done') {
      setRingState('idle');
    }
  }
}

async function runPing() {
  const status = document.getElementById('ipc-status');
  if (!status) return;
  status.textContent = '…';
  try {
    const res = await PingCore();
    status.textContent = `core ${res.round_trip_ms}ms`;
    if (res.ring_state) setRingState(res.ring_state as RingState);
  } catch {
    status.textContent = 'offline';
  }
}

async function submitMessage() {
  const input = document.getElementById('compose-input') as HTMLInputElement | null;
  if (!input || !input.value.trim()) return;
  const text = input.value.trim();
  input.value = '';
  hideSlashSuggest();
  appendMessage('message--user', text);
  setRingState('thinking');
  try {
    const res = await SendMessage(text);
    renderEvents((res.events || []) as StreamEvent[]);
  } catch {
    appendMessage('message--error', 'core offline');
    setRingState('idle');
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

document.querySelector('#app')!.innerHTML = `
  <div class="dock">
    <section class="popup" id="popup" aria-hidden="true" style="--wails-draggable: no-drag">
      <header class="popup-header">
        <div class="popup-brand"><span class="popup-logo">⬡</span><span class="popup-name">SapaLOQ</span></div>
        <button type="button" class="popup-close" id="btn-close" aria-label="Tutup">✕</button>
      </header>
      <div class="popup-hero"><h1>Hai ${iconWave}<br>Ada yang bisa kubantu?</h1></div>
      <div class="popup-body">
        <article class="card card--status"><span class="card-icon">●</span><div><strong>Status</strong><p id="status-badge">idle</p></div></article>
        <article class="card card--chat"><span class="card-icon card-icon--svg">${iconChat}</span><div><strong>Kirim pesan</strong><p>Ngobrol biasa, atau pakai /settings</p></div></article>
        <div class="message-list" id="message-list"></div>
        <p class="ipc-line" id="ipc-status">menghubungkan…</p>
        <p class="ipc-line ipc-line--muted" id="socket-path"></p>
      </div>
      <footer class="popup-compose">
        <div class="compose-wrap">
          <div class="slash-popover" id="slash-popover"></div>
          <input id="compose-input" type="text" placeholder="Ketik pesan…" autocomplete="off" />
        </div>
        <button type="button" class="send-btn" id="send-btn" aria-label="Kirim">↑</button>
      </footer>
    </section>
    <div class="fab-row"><button type="button" class="orb" id="orb" data-state="idle" aria-label="Buka SapaLOQ" style="--wails-draggable: drag"><span class="orb-halo" aria-hidden="true"></span><span class="orb-ring" aria-hidden="true"></span><span class="orb-body" aria-hidden="true"><span class="mascot" aria-hidden="true"><span class="mascot-helmet"></span><span class="mascot-visor"><span class="mascot-eye mascot-eye--l"></span><span class="mascot-eye mascot-eye--r"></span></span><span class="mascot-antenna"></span></span></span><span class="orb-specular" aria-hidden="true"></span><span class="orb-chevron" aria-hidden="true">⌄</span></button></div>
  </div>
`;

void initWindowLayout();
SocketPath().then((path) => {
  const el = document.getElementById('socket-path');
  if (el) el.textContent = path;
});

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

void runPing();
