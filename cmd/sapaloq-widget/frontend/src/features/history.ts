// Chat-history restore + turn rendering, plus the helpers that bind/clear turn
// ids when retrying or deleting a branch. Also owns the topbar history switcher
// (list recent sessions, switch active session, start a new chat).
import { ChatHistory, ContextUsage, ListSessions, NewSession, SwitchSession } from '../../wailsjs/go/main/App';
import type { ChatTurn, ChatUsage, SessionSummary, StreamEvent } from '../core/types';
import { appendCheckpointDivider, appendMessage, appendThinkingBubble, clearMessages, parseTurnContent } from './messages';
import { renderUsage } from './connection';
import { renderTimelineEvent } from './stream';
import {
  getUserGroup,
  nextUserGroup,
  setSessionID,
  getSessionID,
} from '../core/state';

function renderTurn(turn: ChatTurn) {
  if (!turn.content) return;
  // "tool" turns ([Tool results]…) are persisted only so they count toward
  // context usage - they are internal and must never surface as a chat bubble.
  if (turn.role === 'tool') return;
  // "autopilot" turns are SapaLOQ-authored continuation nudges persisted for
  // context accounting; they are never shown to the user.
  if (turn.role === 'autopilot') return;
  // Checkpoint marker turn: render a centered "Checkpoint n" divider followed
  // by a collapsible summary card. The pre-checkpoint bubbles above it are
  // muted by their archived flag (handled per-bubble below). This is the visual
  // seam between archived and live history - the transcript stays complete.
  if (turn.role === 'checkpoint') {
    appendCheckpointDivider(turn.checkpoint_index || 0, turn.content);
    return;
  }
  if (turn.role === 'thinking') {
    appendThinkingBubble(turn.content);
    return;
  }
  const parsed = parseTurnContent(turn.content);
  const archivedClass = turn.archived ? ' message--archived' : '';
  if (turn.role === 'user') {
    nextUserGroup();
    appendMessage(`message--user${archivedClass}`, parsed.text || parsed.attachments.map((item) => item.name).join(', '), getUserGroup(), turn.id, parsed.attachments);
  } else if (turn.role === 'error') appendMessage(`message--error${archivedClass}`, turn.content, getUserGroup(), turn.id);
  else if (turn.role === 'assistant') appendMessage(`message--assistant${archivedClass}`, turn.content, getUserGroup(), turn.id);
}

function parseTimelineAt(iso?: string): number {
  if (!iso) return 0;
  const ts = Date.parse(iso);
  return Number.isNaN(ts) ? 0 : ts;
}

type TimelineItem =
  | { kind: 'turn'; at: number; seq: number; turn: ChatTurn }
  | { kind: 'event'; at: number; event: StreamEvent };

export function buildMergedTimeline(turns: ChatTurn[], events: StreamEvent[]): TimelineItem[] {
  const items: TimelineItem[] = [];
  for (const turn of turns) {
    items.push({ kind: 'turn', at: parseTimelineAt(turn.created_at), seq: turn.seq, turn });
  }
  for (const event of events) {
    if (event.kind !== 'tool_call' && event.kind !== 'task_update') continue;
    items.push({ kind: 'event', at: parseTimelineAt(event.at), event });
  }
  items.sort((a, b) => {
    if (a.at !== b.at) return a.at - b.at;
    if (a.kind === 'turn' && b.kind === 'turn') return a.seq - b.seq;
    return a.kind === 'turn' ? -1 : 1;
  });
  return items;
}

function renderTimeline(items: TimelineItem[]) {
  for (const item of items) {
    if (item.kind === 'turn') renderTurn(item.turn);
    else renderTimelineEvent(item.event, { restore: true });
  }
}

export async function restoreChatHistory() {
  try {
    const history = await ChatHistory();
    setSessionID(history.session_id || getSessionID());
    clearMessages();
    const turns = (history.turns || []) as ChatTurn[];
    const timeline = (history.timeline || []) as StreamEvent[];
    if (timeline.length) {
      renderTimeline(buildMergedTimeline(turns, timeline));
    } else {
      turns.forEach((turn: ChatTurn) => renderTurn(turn));
    }
    renderUsage(history.usage as ChatUsage | undefined);
  } catch {
    // Core may not be ready yet; ping loop will update connection state.
  }
}

// ---- Topbar history switcher --------------------------------------------

const DEFAULT_LABEL = 'SapaLOQ';

function relativeTime(iso: string): string {
  const ts = Date.parse(iso);
  if (Number.isNaN(ts)) return '';
  const diff = Date.now() - ts;
  const min = Math.floor(diff / 60000);
  if (min < 1) return 'baru saja';
  if (min < 60) return `${min}m lalu`;
  const hrs = Math.floor(min / 60);
  if (hrs < 24) return `${hrs}j lalu`;
  const days = Math.floor(hrs / 24);
  if (days < 7) return `${days}h lalu`;
  return new Date(ts).toLocaleDateString();
}

function sessionLabel(session: SessionSummary): string {
  return session.title?.trim() || DEFAULT_LABEL;
}

function setSwitcherLabel(text: string) {
  const el = document.getElementById('history-current');
  if (el) el.textContent = text || DEFAULT_LABEL;
}

export function isHistoryMenuOpen(): boolean {
  const menu = document.getElementById('history-menu');
  return !!menu && !menu.hidden;
}

export function closeHistoryMenu() {
  const menu = document.getElementById('history-menu');
  const btn = document.getElementById('btn-history');
  if (menu) {
    menu.hidden = true;
    menu.setAttribute('aria-hidden', 'true');
  }
  btn?.setAttribute('aria-expanded', 'false');
}

function renderSessionList(sessions: SessionSummary[]) {
  const list = document.getElementById('history-list');
  if (!list) return;
  list.innerHTML = '';
  if (!sessions.length) {
    const empty = document.createElement('div');
    empty.className = 'history-empty';
    empty.textContent = 'Belum ada riwayat chat.';
    list.appendChild(empty);
    return;
  }
  for (const session of sessions) {
    const item = document.createElement('button');
    item.type = 'button';
    item.className = 'history-item';
    item.setAttribute('role', 'menuitem');
    item.dataset.sessionId = session.id;
    item.dataset.active = session.active ? 'true' : 'false';

    const title = document.createElement('span');
    title.className = 'history-item-title';
    title.textContent = sessionLabel(session);
    item.appendChild(title);

    const meta = document.createElement('span');
    meta.className = 'history-item-meta';
    const rel = relativeTime(session.updated_at);
    meta.textContent = `${session.turn_count} pesan${rel ? ` · ${rel}` : ''}`;
    item.appendChild(meta);

    if (session.active) {
      const dot = document.createElement('span');
      dot.className = 'history-item-dot';
      dot.title = 'Sesi aktif';
      item.appendChild(dot);
    }
    list.appendChild(item);
  }
}

// loadSessionList refreshes the dropdown contents and the switcher label from
// the active session.
export async function loadSessionList() {
  try {
    const result = await ListSessions();
    const sessions = (result.sessions || []) as SessionSummary[];
    renderSessionList(sessions);
    const active = sessions.find((session) => session.active);
    if (active) setSwitcherLabel(sessionLabel(active));
  } catch {
    renderSessionList([]);
  }
}

export async function openHistoryMenu() {
  const menu = document.getElementById('history-menu');
  const btn = document.getElementById('btn-history');
  if (!menu) return;
  menu.hidden = false;
  menu.setAttribute('aria-hidden', 'false');
  btn?.setAttribute('aria-expanded', 'true');
  await loadSessionList();
}

export async function toggleHistoryMenu() {
  if (isHistoryMenuOpen()) closeHistoryMenu();
  else await openHistoryMenu();
}

async function refreshAfterSessionChange() {
  await restoreChatHistory();
  await loadSessionList();
  try {
    renderUsage((await ContextUsage()) as ChatUsage);
  } catch {
    // usage refresh is best-effort; the chat view already swapped.
  }
}

// switchSession activates an existing session and reloads the chat + usage so
// the widget reflects the chosen conversation.
export async function switchSession(sessionID: string) {
  if (!sessionID || sessionID === getSessionID()) {
    closeHistoryMenu();
    return;
  }
  try {
    const activeID = await SwitchSession(sessionID);
    setSessionID(activeID || sessionID);
  } catch {
    return;
  } finally {
    closeHistoryMenu();
  }
  await refreshAfterSessionChange();
}

// startNewSession spins up a fresh active chat and clears the view.
export async function startNewSession() {
  try {
    const newID = await NewSession();
    if (newID) setSessionID(newID);
  } catch {
    return;
  } finally {
    closeHistoryMenu();
  }
  clearMessages();
  setSwitcherLabel(DEFAULT_LABEL);
  await refreshAfterSessionChange();
}

export async function bindLatestGroupTurnID() {
  try {
    const history = await ChatHistory();
    const turns = (history.turns || []) as ChatTurn[];
    const user = [...turns].reverse().find((turn) => turn.role === 'user');
    if (!user) return 0;
    document.querySelectorAll<HTMLElement>(`.message[data-group="${getUserGroup()}"]`).forEach((item) => {
      item.dataset.turnId = `${user.id}`;
    });
    return user.id;
  } catch {
    return 0;
  }
}

// removeRepliesAfterTurn clears stale assistant/thinking/tool/error bubbles that
// belong to the retried user turn (and everything after it) so the regenerated
// response can stream into the same group instead of stacking on top of the old
// reply. Returns the group id of the retried user message so streamed events
// render in the correct place.
export function removeRepliesAfterTurn(turnID: number): number {
  const user = document.querySelector<HTMLElement>(`.message--user[data-turn-id="${turnID}"]`);
  if (!user) return getUserGroup();
  const group = Number(user.dataset.group || getUserGroup());
  document.querySelectorAll<HTMLElement>('#message-list > .message').forEach((item) => {
    const itemGroup = Number(item.dataset.group || 0);
    if (itemGroup < group) return;
    if (item === user) return;
    if (itemGroup === group && item.classList.contains('message--user')) return;
    item.remove();
  });
  return group;
}
