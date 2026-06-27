// task-monitor-overlay.ts is the "Planner & Agent" pop-up. Clicking a runtime
// strip pill opens this modal, which shows each sub-agent's live activity
// (thinking / tool calls / assistant text) streamed from the per-task progress
// JSONL, plus the planner's plan.md in a dedicated sub-tab. It polls
// TaskInspect incrementally (afterLine = last event_count) while the actor is
// active and stops when the pop-up closes or the task settles.
//
// The overlay is a singleton (only one open at a time) and dismissable via the
// X button, Escape, or a backdrop click - mirroring the image-preview overlay
// but as a centered dialog panel so the chat stays visible behind it.

import { RuntimeStatus, TaskInspect } from '../../wailsjs/go/main/App';
import type { main } from '../../wailsjs/go/models';
import { renderMarkdown } from './markdown';
import {
  mountTranscriptPane,
  syncTranscriptPane,
  emptyTranscriptState,
  type TranscriptEntry,
} from './transcript';

type Tab = 'planner' | 'agent';
type SubTab = 'activity' | 'plan';

type TaskInspectResult = main.taskInspectResult & { transcript?: TranscriptEntry[] };

const POLL_INTERVAL_MS = 2000;
const OVERLAY_ID = 'task-monitor-overlay';

interface ActorState {
  taskID: string;
  active: boolean;
  lastEventCount: number;
  // Cached last inspect so a tab switch re-renders without a refetch.
  last: TaskInspectResult | null;
  // Number of coalesced activity entries already mounted in the DOM.
  renderedEntryCount: number;
  transcript: TranscriptEntry[];
}

let overlay: HTMLDivElement | null = null;
let pollTimer: ReturnType<typeof setInterval> | null = null;
let activeTab: Tab = 'planner';
let activeSubTab: SubTab = 'activity';
let escapeHandler: ((e: KeyboardEvent) => void) | null = null;
// When the overlay is opened from a specific chat bubble we PIN that exact
// task into its tab instead of resolving the tab's task from RuntimeStatus
// (which only ever surfaces ONE actor per role - the latest - so multiple
// spawned agents are otherwise invisible). The pin overrides role resolution
// for its tab; the other tab still resolves normally.
let pinnedTaskID = '';
let pinnedTab: Tab | null = null;
const actorState: Record<Tab, ActorState> = {
  planner: { taskID: '', active: false, lastEventCount: 0, last: null, renderedEntryCount: 0, transcript: [] },
  agent: { taskID: '', active: false, lastEventCount: 0, last: null, renderedEntryCount: 0, transcript: [] },
};

// When true, new activity sticks to the bottom. Flips false as soon as the
// reader scrolls away from the bottom; flips back when they return to the end.
let scrollFollow = true;

// tabForRole maps any sub-agent role to one of the two overlay tabs.
function tabForRole(role: string): Tab {
  return role === 'planner' ? 'planner' : 'agent';
}

function isSettled(actor: { status?: string; phase?: string } | undefined): boolean {
  if (!actor) return true;
  const status = (actor.status || '').toLowerCase();
  const phase = (actor.phase || '').toLowerCase();
  return status === 'done' || status === 'failed' || status === 'stopped' ||
    phase === 'finalizing' || phase === 'exited';
}

function roleLabel(role: Tab | string): string {
  if (role === 'agent' || role === 'task-runner') return 'Agent';
  if (role === 'planner') return 'Planner';
  if (role === 'scribe') return 'Scribe';
  return role ? String(role) : 'Actor';
}

// openTaskMonitor opens the pop-up. Three call shapes:
//   - { tab }                 → activate that tab, resolve its task by role
//                               (the runtime-strip pill path).
//   - { taskID, role }        → PIN that exact task into its role's tab (the
//                               chat-bubble path) so a specific spawned agent
//                               is shown even when several exist.
// Safe to call when an overlay is already open (it just switches/pins).
export async function openTaskMonitor(opts?: { tab?: Tab; taskID?: string; role?: string }) {
  if (opts?.taskID) {
    const tab = tabForRole(opts.role || 'task-runner');
    // A new pin targeting a different task must reset that tab's incremental
    // cursor so the stream is re-fetched from the start, not diffed against the
    // previous task's event count.
    if (pinnedTaskID !== opts.taskID) {
      const st = actorState[tab];
      st.taskID = opts.taskID;
      st.lastEventCount = 0;
      st.transcript = [];
      st.last = null;
      st.renderedEntryCount = 0;
    }
    pinnedTaskID = opts.taskID;
    pinnedTab = tab;
    activeTab = tab;
  } else if (opts?.tab) {
    activeTab = opts.tab;
    // Opening a tab via its pill clears any pin on that tab so it tracks the
    // live role actor again.
    if (pinnedTab === opts.tab) {
      pinnedTaskID = '';
      pinnedTab = null;
    }
  }
  if (overlay) {
    void refreshAndRender();
    return;
  }
  // A fresh open always starts on the activity view; a stale sub-tab choice
  // from a previous open (e.g. "Plan") must not bleed into this one.
  activeSubTab = 'activity';
  buildOverlay();
  attachDismissHandlers();
  await refreshAndRender();
  startPoll();
}

export function closeTaskMonitor() {
  stopPoll();
  if (escapeHandler) {
    document.removeEventListener('keydown', escapeHandler);
    escapeHandler = null;
  }
  // Clear any chat-bubble pin so the next open starts fresh (a stale pin must
  // not silently override the pill path on the following open).
  pinnedTaskID = '';
  pinnedTab = null;
  for (const tab of ['planner', 'agent'] as Tab[]) {
    actorState[tab] = { taskID: '', active: false, lastEventCount: 0, last: null, renderedEntryCount: 0, transcript: [] };
  }
  scrollFollow = true;
  overlay?.remove();
  overlay = null;
}

function stopPoll() {
  if (pollTimer) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
}

function startPoll() {
  stopPoll();
  pollTimer = setInterval(() => void refreshAndRender(), POLL_INTERVAL_MS);
}

function buildOverlay() {
  const el = document.createElement('div');
  el.id = OVERLAY_ID;
  el.className = 'task-monitor-overlay';
  el.setAttribute('role', 'dialog');
  el.setAttribute('aria-modal', 'true');
  el.setAttribute('aria-label', 'Sub-agents');

  const panel = document.createElement('div');
  panel.className = 'task-monitor-panel';
  // Clicks inside the panel must NOT bubble to the backdrop dismiss handler.
  panel.addEventListener('click', (e) => e.stopPropagation());

  const header = document.createElement('div');
  header.className = 'task-monitor-header';
  const title = document.createElement('strong');
  title.textContent = 'Sub-agents';
  const closeBtn = document.createElement('button');
  closeBtn.type = 'button';
  closeBtn.className = 'task-monitor-close';
  closeBtn.setAttribute('aria-label', 'Close');
  closeBtn.innerHTML = '&times;';
  closeBtn.addEventListener('click', () => closeTaskMonitor());
  header.append(title, closeBtn);

  const tabs = document.createElement('div');
  tabs.className = 'task-monitor-tabs';
  const plannerTab = document.createElement('button');
  plannerTab.type = 'button';
  plannerTab.className = 'task-monitor-tab';
  plannerTab.dataset.tab = 'planner';
  plannerTab.textContent = 'Planner';
  plannerTab.addEventListener('click', () => switchTab('planner'));
  const agentTab = document.createElement('button');
  agentTab.type = 'button';
  agentTab.className = 'task-monitor-tab';
  agentTab.dataset.tab = 'agent';
  agentTab.textContent = 'Agent';
  agentTab.addEventListener('click', () => switchTab('agent'));
  tabs.append(plannerTab, agentTab);

  const subTabs = document.createElement('div');
  subTabs.className = 'task-monitor-subtabs';

  const body = document.createElement('div');
  body.className = 'task-monitor-body';
  body.addEventListener('scroll', () => {
    scrollFollow = isNearBottom(body);
  }, { passive: true });

  panel.append(header, tabs, subTabs, body);
  el.append(panel);
  // Mount INSIDE the chat popup (not document.body) so the overlay is clipped
  // to the popup's rounded, overflow-hidden bounds and its backdrop only dims
  // the chat surface - not the whole transparent widget window. Mounting on
  // body made the modal escape the popup and float as an isolated black box
  // over the desktop. Fall back to body if the popup is somehow absent.
  const host = document.getElementById('popup') || document.body;
  host.append(el);
  overlay = el;
}

function attachDismissHandlers() {
  // Backdrop click: the panel stops propagation, so a click that reaches the
  // overlay root landed on the backdrop.
  overlay?.addEventListener('click', () => closeTaskMonitor());
  escapeHandler = (e: KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.preventDefault();
      closeTaskMonitor();
    }
  };
  document.addEventListener('keydown', escapeHandler);
}

function switchTab(tab: Tab) {
  activeTab = tab;
  activeSubTab = 'activity';
  actorState[tab].renderedEntryCount = 0;
  scrollFollow = true;
  // Clicking a tab button shows that role's LIVE actor, so drop any pin on it.
  if (pinnedTab === tab) {
    pinnedTaskID = '';
    pinnedTab = null;
  }
  void refreshAndRender();
}

function switchSubTab(sub: SubTab) {
  activeSubTab = sub;
  actorState[activeTab].renderedEntryCount = 0;
  scrollFollow = true;
  void renderCurrent();
}

// refreshAndRender fetches runtime status (to resolve the current actor ids +
// liveness) then the active tab's inspect, and re-renders. The non-active tab
// is fetched lazily on switch to avoid double IPC per poll.
async function refreshAndRender() {
  if (!overlay) return;
  let status: Awaited<ReturnType<typeof RuntimeStatus>> | null = null;
  try {
    status = (await RuntimeStatus()) as unknown as Awaited<ReturnType<typeof RuntimeStatus>>;
  } catch {
    status = null;
  }
  const plannerActor = status?.actors?.find((a) => a.role === 'planner');
  const agentActor = status?.actors?.find((a) => a.role === 'task-runner');
  applyResolvedActor('planner', plannerActor?.id || '', !!plannerActor && !isSettled(plannerActor));
  applyResolvedActor('agent', agentActor?.id || '', !!agentActor && !isSettled(agentActor));

  // Pin override: force the pinned tab onto its specific task regardless of
  // which actor the role currently resolves to. Liveness is derived from the
  // task's own inspect status after fetch (a pinned task may already be
  // terminal or no longer the role's active actor).
  if (pinnedTaskID && pinnedTab) {
    const st = actorState[pinnedTab];
    if (st.taskID !== pinnedTaskID) {
      st.taskID = pinnedTaskID;
      st.lastEventCount = 0;
      st.transcript = [];
      st.last = null;
      st.renderedEntryCount = 0;
    }
  }

  await fetchTab(activeTab);
  renderTabs();
  renderCurrent();
}

// applyResolvedActor sets a tab's task from RuntimeStatus, resetting the
// incremental cursor when the resolved task id changes (a new actor for that
// role) so the new task's stream is fetched from the start. A pinned tab is
// left untouched (the pin owns its task id).
function applyResolvedActor(tab: Tab, taskID: string, active: boolean) {
  if (pinnedTab === tab && pinnedTaskID) return;
  const st = actorState[tab];
  if (st.taskID !== taskID) {
    st.taskID = taskID;
    st.lastEventCount = 0;
    st.transcript = [];
    st.last = null;
    st.renderedEntryCount = 0;
  }
  st.active = active;
}

async function fetchTab(tab: Tab) {
  const state = actorState[tab];
  if (!state.taskID) {
    state.last = null;
    state.lastEventCount = 0;
    state.transcript = [];
    return;
  }
  try {
    const res = (await TaskInspect(state.taskID, 0)) as unknown as TaskInspectResult;
    state.transcript = (res.transcript || []) as TranscriptEntry[];
    state.last = res;
    state.lastEventCount = res.event_count ?? state.lastEventCount;
    if (pinnedTab === tab && pinnedTaskID) {
      const s = (res.status || '').toLowerCase();
      state.active = !(s === 'done' || s === 'failed' || s === 'stopped');
    }
  } catch {
    // stale task id
  }
}

function renderTabs() {
  if (!overlay) return;
  overlay.querySelectorAll('.task-monitor-tab').forEach((btn) => {
    const b = btn as HTMLButtonElement;
    b.classList.toggle('is-active', b.dataset.tab === activeTab);
    const settled = !actorState[b.dataset.tab as Tab].active;
    b.classList.toggle('is-idle', settled);
  });
}

function renderCurrent() {
  if (!overlay) return;
  const subTabs = overlay.querySelector('.task-monitor-subtabs') as HTMLElement | null;
  const body = overlay.querySelector('.task-monitor-body') as HTMLElement | null;
  if (!subTabs || !body) return;

  subTabs.replaceChildren();
  subTabs.hidden = true;

  const state = actorState[activeTab];
  const res = state.last;

  const showPlanTab = activeTab === 'planner' || (res?.plan_task_id && res?.plan) ? true : false;
  if (showPlanTab) {
    subTabs.hidden = false;
    const act = document.createElement('button');
    act.type = 'button';
    act.className = 'task-monitor-subtab';
    act.dataset.sub = 'activity';
    act.textContent = 'Activity';
    act.addEventListener('click', () => switchSubTab('activity'));
    const plan = document.createElement('button');
    plan.type = 'button';
    plan.className = 'task-monitor-subtab';
    plan.dataset.sub = 'plan';
    plan.textContent = 'Plan';
    plan.addEventListener('click', () => switchSubTab('plan'));
    act.classList.toggle('is-active', activeSubTab === 'activity');
    plan.classList.toggle('is-active', activeSubTab === 'plan');
    subTabs.append(act, plan);
  } else {
    activeSubTab = 'activity';
  }

  if (!state.taskID || !res) {
    body.replaceChildren(emptyState(`${roleLabel(activeTab)} tidak aktif`));
    state.renderedEntryCount = 0;
    return;
  }

  const needsFullPaint = !body.querySelector('.task-monitor-headline-wrap')
    || body.dataset.taskId !== state.taskID
    || body.dataset.view !== activeSubTab;
  if (needsFullPaint) {
    body.replaceChildren();
    body.dataset.taskId = state.taskID;
    body.dataset.view = activeSubTab;
    state.renderedEntryCount = 0;
    scrollFollow = true;
    body.append(buildHeaderLine(res, state.active));
    if (activeSubTab === 'plan') {
      body.append(buildPlanPane(res));
    } else {
      mountActivityPane(body, state, res);
    }
    if (scrollFollow) scrollToBottom(body);
    return;
  }

  updateHeaderLine(body.querySelector('.task-monitor-headline-wrap') as HTMLElement, res, state.active);
  if (activeSubTab === 'plan') {
    const planPane = body.querySelector('.task-monitor-plan');
    if (!planPane) {
      body.querySelector('.transcript-pane')?.remove();
      body.append(buildPlanPane(res));
      state.renderedEntryCount = 0;
    }
  } else {
    body.querySelector('.task-monitor-plan')?.remove();
    syncActivityPane(body, state, res);
  }
  if (scrollFollow) scrollToBottom(body);
}

function isNearBottom(el: HTMLElement): boolean {
  const threshold = 64;
  return el.scrollHeight - el.scrollTop - el.clientHeight <= threshold;
}

function scrollToBottom(el: HTMLElement): void {
  el.scrollTop = el.scrollHeight;
}

function buildHeaderLine(res: TaskInspectResult, active: boolean): HTMLElement {
  const wrap = document.createElement('div');
  wrap.className = 'task-monitor-headline-wrap';

  const line = document.createElement('div');
  line.className = 'task-monitor-headline';
  const status = document.createElement('span');
  status.className = 'task-monitor-status';
  status.dataset.status = res.status || (active ? 'active' : 'idle');
  status.textContent = statusLabel(res.status, active);
  const role = document.createElement('span');
  role.className = 'task-monitor-role';
  role.textContent = roleLabel(res.role);
  line.append(status, role);
  wrap.append(line);

  // The task prompt can be very long (a planner's full planning brief). Render
  // it as a collapsed, line-clamped block instead of an unbounded inline span
  // so it never grows into a wall of text that pushes the tabs/overlap. The
  // full text is still reachable by expanding the <details>.
  const task = (res.task || '(no task text)').trim();
  const details = document.createElement('details');
  details.className = 'task-monitor-task-details';
  const summary = document.createElement('summary');
  summary.textContent = truncateForSummary(task);
  const body = document.createElement('div');
  body.className = 'task-monitor-task-body';
  body.textContent = task;
  details.append(summary, body);
  wrap.append(details);

  if (res.question) {
    const q = document.createElement('div');
    q.className = 'task-monitor-question';
    q.textContent = '❓ ' + res.question;
    wrap.append(q);
  }
  return wrap;
}

function updateHeaderLine(wrap: HTMLElement, res: TaskInspectResult, active: boolean) {
  const status = wrap.querySelector('.task-monitor-status') as HTMLElement | null;
  if (status) {
    status.dataset.status = res.status || (active ? 'active' : 'idle');
    status.textContent = statusLabel(res.status, active);
  }
  const role = wrap.querySelector('.task-monitor-role');
  if (role) role.textContent = roleLabel(res.role);
  const task = (res.task || '(no task text)').trim();
  const details = wrap.querySelector('.task-monitor-task-details') as HTMLDetailsElement | null;
  if (details) {
    const summary = details.querySelector('summary');
    if (summary) summary.textContent = truncateForSummary(task);
    const body = details.querySelector('.task-monitor-task-body');
    if (body) body.textContent = task;
  }
  const question = wrap.querySelector('.task-monitor-question') as HTMLElement | null;
  if (res.question) {
    if (question) question.textContent = '❓ ' + res.question;
    else {
      const q = document.createElement('div');
      q.className = 'task-monitor-question';
      q.textContent = '❓ ' + res.question;
      wrap.append(q);
    }
  } else {
    question?.remove();
  }
}

// truncateForSummary produces a one-line preview for the collapsed task
// <details> summary. Keeps the header compact; the full text lives in the
// expandable body.
function truncateForSummary(text: string): string {
  const oneLine = text.replace(/\s+/g, ' ').trim();
  if (oneLine.length <= 100) return oneLine || '(no task text)';
  return oneLine.slice(0, 100) + '…';
}

function statusLabel(status: string, active: boolean): string {
  switch (status) {
    case 'pending': return '🕓 dijadwalkan';
    case 'in_progress': return active ? '⏳ sedang bekerja' : '⏳ berhenti';
    case 'done': return '✅ selesai';
    case 'failed': return '⚠️ gagal';
    case 'awaiting_clarification': return '❓ butuh keputusan';
    case 'stopping': return '⏹️ menghentikan';
    case 'stopped': return '⏹️ dihentikan';
    default: return active ? '⏳ aktif' : 'idle';
  }
}

function buildPlanPane(res: TaskInspectResult): HTMLElement {
  const pane = document.createElement('div');
  pane.className = 'task-monitor-plan';
  if (!res.plan || !res.plan.trim()) {
    pane.append(emptyState('Belum ada plan'));
    return pane;
  }
  const md = document.createElement('div');
  md.className = 'task-monitor-markdown';
  md.append(renderMarkdown(res.plan));
  pane.append(md);
  return pane;
}

function mountActivityPane(body: HTMLElement, state: ActorState, res: TaskInspectResult) {
  mountTranscriptPane(
    body,
    state,
    (res.transcript || state.transcript || []) as TranscriptEntry[],
    'Belum ada aktivitas',
    'monitor',
    'task-monitor-empty',
  );
}

function syncActivityPane(body: HTMLElement, state: ActorState, res: TaskInspectResult) {
  syncTranscriptPane(
    body,
    state,
    (res.transcript || state.transcript || []) as TranscriptEntry[],
    'Belum ada aktivitas',
    'monitor',
    'task-monitor-empty',
  );
}

function emptyState(message: string): HTMLElement {
  return emptyTranscriptState(message, 'task-monitor-empty');
}
