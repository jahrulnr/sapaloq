// Chat-history restore via BE-driven transcript API.
import { ChatHistory, ContextUsage, ListSessions, NewSession, SwitchSession } from '../../wailsjs/go/main/App';
import type { ChatUsage, SessionSummary } from '../core/types';
import { applyChatResetFromBE } from './apply-session-reset';
import { clearMessages } from './messages';
import { renderUsage } from './connection';
import { mountChatTranscript, resetChatTranscriptState } from './transcript-pane';
import {
  getUserGroup,
  setSessionID,
  getSessionID,
} from '../core/state';

export async function restoreChatHistory() {
  try {
    const history = await ChatHistory();
    setSessionID(history.session_id || getSessionID());
    clearMessages();
    resetChatTranscriptState();
    mountChatTranscript(history.transcript || []);
    renderUsage(history.usage as ChatUsage | undefined);
    return true;
  } catch {
    return false;
  }
}

export function bindLatestGroupTurnID() {
  const list = document.getElementById('message-list');
  if (!list) return;
  const users = Array.from(list.querySelectorAll<HTMLElement>('.message--user, .transcript-user'));
  const last = users.at(-1);
  if (last?.dataset.turnId) return;
  const assistants = Array.from(list.querySelectorAll<HTMLElement>('.message--assistant, .transcript-text'));
  const lastAssistant = assistants.at(-1);
  if (last?.dataset.turnId) return;
  void ChatHistory().then((history) => {
    const turns = (history.transcript || []).filter((e) => e.kind === 'user');
    const lastUser = turns.at(-1);
    if (lastUser?.turn_id && last) last.dataset.turnId = `${lastUser.turn_id}`;
    const lastText = (history.transcript || []).filter((e) => e.kind === 'text').at(-1);
    if (lastText?.turn_id && lastAssistant) lastAssistant.dataset.turnId = `${lastText.turn_id}`;
  });
}

export function removeRepliesAfterTurn(turnID: number): number {
  const list = document.getElementById('message-list');
  if (!list) return getUserGroup();
  const target = list.querySelector<HTMLElement>(`[data-turn-id="${turnID}"]`);
  if (!target) return getUserGroup();
  const group = Number(target.dataset.group || getUserGroup());
  let seen = false;
  Array.from(list.querySelectorAll<HTMLElement>('.transcript-entry, .message')).forEach((node) => {
    if (node === target || node.contains(target)) seen = true;
    if (seen && node !== target && !target.contains(node)) node.remove();
    else if (!seen && node.contains(target)) {
      let n: HTMLElement | null = target;
      while (n?.nextElementSibling) {
        const next = n.nextElementSibling as HTMLElement;
        next.remove();
      }
    }
  });
  const pane = list.querySelector('.transcript-pane');
  if (pane && target.parentElement === pane) {
    let found = false;
    Array.from(pane.children).forEach((child) => {
      if (child === target) found = true;
      else if (found) child.remove();
    });
  }
  return group;
}

export async function refreshContextUsage() {
  try {
    const usage = await ContextUsage();
    renderUsage(usage as ChatUsage | undefined);
  } catch {
    // core offline
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

export function setSwitcherLabel(text: string) {
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

export async function loadSessionList() {
  try {
    const sessions = await loadSessionListInner();
    renderSessionList(sessions);
    const active = sessions.find((session) => session.active);
    if (active) setSwitcherLabel(sessionLabel(active));
  } catch {
    renderSessionList([]);
  }
}

async function loadSessionListInner(): Promise<SessionSummary[]> {
  const res = await ListSessions();
  return (res.sessions || []) as SessionSummary[];
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
    // best-effort
  }
}

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

export async function startNewSession() {
  try {
    const res = await NewSession();
    if (!res.reset) return;
    applyChatResetFromBE({
      session_id: res.session_id,
      entries: res.transcript,
      reset: true,
    });
    setSwitcherLabel(DEFAULT_LABEL);
    void loadSessionList();
    try {
      renderUsage((await ContextUsage()) as ChatUsage);
    } catch {
      // best-effort
    }
  } catch {
    return;
  } finally {
    closeHistoryMenu();
  }
}
