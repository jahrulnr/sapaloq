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

type Tab = 'planner' | 'agent';
type SubTab = 'activity' | 'plan';

type TaskInspectEvent = main.taskInspectEvent;
type TaskInspectResult = main.taskInspectResult;

const POLL_INTERVAL_MS = 2000;
const OVERLAY_ID = 'task-monitor-overlay';

interface ActorState {
  taskID: string;
  active: boolean;
  lastEventCount: number;
  // Accumulated progress tail across incremental TaskInspect polls. Each poll
  // only returns lines after lastEventCount, so replacing the prior slice
  // makes the activity pane flash empty on the next poll.
  events: TaskInspectEvent[];
  // Cached last inspect so a tab switch re-renders without a refetch.
  last: TaskInspectResult | null;
  // Number of coalesced activity entries already mounted in the DOM.
  renderedEntryCount: number;
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
  planner: { taskID: '', active: false, lastEventCount: 0, events: [], last: null, renderedEntryCount: 0 },
  agent: { taskID: '', active: false, lastEventCount: 0, events: [], last: null, renderedEntryCount: 0 },
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
      st.events = [];
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
    actorState[tab] = { taskID: '', active: false, lastEventCount: 0, events: [], last: null, renderedEntryCount: 0 };
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
      st.events = [];
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
    st.events = [];
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
    state.events = [];
    return;
  }
  try {
    const res = (await TaskInspect(state.taskID, state.lastEventCount)) as unknown as TaskInspectResult;
    const delta = res.events || [];
    if (delta.length > 0) state.events.push(...delta);
    res.events = state.events;
    state.last = res;
    state.lastEventCount = res.event_count ?? state.lastEventCount;
    // For a pinned tab the RuntimeStatus role-actor liveness does not apply
    // (the pin may target a different/older task), so derive liveness from the
    // task's own inspect status.
    if (pinnedTab === tab && pinnedTaskID) {
      const s = (res.status || '').toLowerCase();
      state.active = !(s === 'done' || s === 'failed' || s === 'stopped');
    }
  } catch {
    // A stale id (task just ended + GC'd) leaves the last snapshot in place;
    // the header will show "tidak aktif" via the empty-taskID path next poll.
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
      body.querySelector('.task-monitor-activity')?.remove();
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
  const coalesced = coalesceEvents(res.events || []);
  if (coalesced.length === 0) {
    body.append(emptyState('Belum ada aktivitas'));
    state.renderedEntryCount = 0;
    return;
  }
  const pane = document.createElement('div');
  pane.className = 'task-monitor-activity';
  for (const entry of coalesced) pane.append(renderActivityEntry(entry));
  body.append(pane);
  state.renderedEntryCount = coalesced.length;
}

function syncActivityPane(body: HTMLElement, state: ActorState, res: TaskInspectResult) {
  const coalesced = coalesceEvents(res.events || []);
  let pane = body.querySelector('.task-monitor-activity') as HTMLElement | null;
  body.querySelector('.task-monitor-empty')?.remove();

  if (coalesced.length === 0) {
    pane?.remove();
    if (!body.querySelector('.task-monitor-empty')) {
      body.append(emptyState('Belum ada aktivitas'));
    }
    state.renderedEntryCount = 0;
    return;
  }

  if (!pane) {
    pane = document.createElement('div');
    pane.className = 'task-monitor-activity';
    body.append(pane);
    state.renderedEntryCount = 0;
  }

  const prev = state.renderedEntryCount;
  const patchEnd = Math.min(prev, coalesced.length);
  for (let i = 0; i < patchEnd; i++) {
    const el = pane.children[i] as HTMLElement | undefined;
    if (!el || el.dataset.entryKind !== coalesced[i].kind) {
      while (pane.children.length > i) pane.lastChild?.remove();
      state.renderedEntryCount = i;
      for (let j = i; j < coalesced.length; j++) pane.append(renderActivityEntry(coalesced[j]));
      state.renderedEntryCount = coalesced.length;
      return;
    }
    patchActivityEntry(el, coalesced[i]);
  }

  for (let i = prev; i < coalesced.length; i++) {
    pane.append(renderActivityEntry(coalesced[i]));
  }
  while (pane.children.length > coalesced.length) pane.lastChild?.remove();
  state.renderedEntryCount = coalesced.length;
}

function formatMonitorPayload(value: string): string {
  const trimmed = value.trim();
  if (!trimmed) return trimmed;
  try {
    return JSON.stringify(JSON.parse(trimmed), null, 2);
  } catch {
    return value;
  }
}

function patchActivityEntry(el: HTMLElement, entry: ActivityEntry) {
  if (entry.kind === 'text' || entry.kind === 'thinking') {
    const body = el.querySelector('.task-monitor-entry-body');
    if (body) body.replaceChildren(renderMarkdown(entry.text));
    return;
  }
  if (entry.kind === 'tool') {
    patchToolEntry(el, entry);
    return;
  }
  if (entry.kind === 'status') {
    el.textContent = entry.label;
  }
}

function patchToolEntry(wrap: HTMLElement, entry: Extract<ActivityEntry, { kind: 'tool' }>) {
  const label = wrap.firstChild;
  if (label?.nodeType === 3) {
    const marker = wrap.classList.contains('is-open') ? '⌄' : '›';
    (label as Text).nodeValue = `${marker}  $ ${entry.name}  ·  ${entry.status || 'running'}`;
  }
  const sections = wrap.querySelectorAll('.task-monitor-tool-section');
  if (sections.length < 2 || entry.response === undefined) return;
  const response = entry.response || '(no output)';
  const responseSection = sections[1] as HTMLElement;
  const code = responseSection.querySelector('code');
  if (code) code.textContent = formatMonitorPayload(response);
  if (entry.status) responseSection.dataset.status = entry.status;
}

type ActivityEntry =
  | { kind: 'thinking'; text: string }
  | { kind: 'text'; text: string }
  | { kind: 'tool'; id: string; name: string; args: string; response?: string; status?: string }
  | { kind: 'status'; label: string };

function coalesceEvents(events: TaskInspectEvent[]): ActivityEntry[] {
  const out: ActivityEntry[] = [];
  let textBuf = '';
  let thinkBuf = '';
  const flushText = () => {
    if (textBuf.trim()) out.push({ kind: 'text', text: textBuf });
    textBuf = '';
  };
  const flushThinking = () => {
    if (thinkBuf.trim()) out.push({ kind: 'thinking', text: thinkBuf });
    thinkBuf = '';
  };
  for (const ev of events) {
    switch (ev.kind) {
      case 'response_delta':
        flushThinking();
        textBuf += ev.delta || '';
        break;
      case 'thinking_delta':
        flushText();
        thinkBuf += ev.delta || '';
        break;
      case 'tool_call':
        flushText();
        flushThinking();
        out.push({ kind: 'tool', id: ev.tool_id || '', name: ev.tool_name || 'tool', args: ev.tool_arguments || '' });
        break;
      case 'tool_update': {
        flushText();
        flushThinking();
        const match = [...out].reverse().find((item): item is Extract<ActivityEntry, { kind: 'tool' }> =>
          item.kind === 'tool' && (ev.tool_id ? item.id === ev.tool_id : item.name === (ev.tool_name || 'tool')) && item.response === undefined);
        if (match) {
          match.response = ev.tool_result || ev.error || '';
          match.status = ev.error ? 'failed' : (ev.status || 'completed');
        } else {
          out.push({
            kind: 'tool', id: ev.tool_id || '', name: ev.tool_name || 'tool', args: ev.tool_arguments || '',
            response: ev.tool_result || ev.error || '', status: ev.error ? 'failed' : (ev.status || 'completed'),
          });
        }
        break;
      }
      case 'turn_boundary':
        flushText();
        flushThinking();
        break;
      case 'task_update':
        flushText();
        flushThinking();
        break;
      case 'status':
        if (ev.status && ev.status !== 'working') {
          flushText();
          flushThinking();
          out.push({ kind: 'status', label: ev.status });
        }
        break;
      case 'error':
        flushText();
        flushThinking();
        out.push({ kind: 'status', label: 'error: ' + (ev.error || 'unknown') });
        break;
      default:
        break;
    }
  }
  flushText();
  flushThinking();
  return out;
}

function renderActivityEntry(entry: ActivityEntry): HTMLElement {
  if (entry.kind === 'thinking') {
    const details = document.createElement('details');
    details.className = 'task-monitor-entry task-monitor-thinking is-collapsed';
    details.dataset.entryKind = 'thinking';
    const sum = document.createElement('summary');
    sum.textContent = '💭 thinking';
    const body = document.createElement('div');
    body.className = 'task-monitor-entry-body';
    body.append(renderMarkdown(entry.text));
    details.append(sum, body);
    return details;
  }
  if (entry.kind === 'text') {
    const wrap = document.createElement('div');
    wrap.className = 'task-monitor-entry task-monitor-text';
    wrap.dataset.entryKind = 'text';
    const label = document.createElement('span');
    label.className = 'task-monitor-entry-label';
    label.textContent = 'Assistant';
    const body = document.createElement('div');
    body.className = 'task-monitor-entry-body';
    body.append(renderMarkdown(entry.text));
    wrap.append(label, body);
    return wrap;
  }
  if (entry.kind === 'tool') {
    const waiting = entry.response === undefined;
    const wrap = document.createElement('div');
    wrap.className = 'task-monitor-entry task-monitor-tool';
    wrap.dataset.entryKind = 'tool';
    if (entry.id) wrap.dataset.toolId = entry.id;
    wrap.classList.toggle('is-open', waiting);
    wrap.setAttribute('role', 'button');
    wrap.setAttribute('tabindex', '0');
    wrap.setAttribute('aria-expanded', String(waiting));
    const label = document.createTextNode('');
    const paintLabel = () => {
      const marker = wrap.classList.contains('is-open') ? '⌄' : '›';
      label.nodeValue = `${marker}  $ ${entry.name}  ·  ${entry.status || 'running'}`;
    };
    paintLabel();
    const body = document.createElement('div');
    body.className = 'task-monitor-tool-body';
    body.hidden = !waiting;
    body.append(toolMonitorSection('Request', entry.args || 'No arguments'));
    const response = waiting ? 'Waiting for response…' : entry.response || '(no output)';
    body.append(toolMonitorSection('Response', response, entry.status));
    const toggle = () => {
      const open = !wrap.classList.contains('is-open');
      wrap.classList.toggle('is-open', open);
      wrap.setAttribute('aria-expanded', String(open));
      paintLabel();
      body.hidden = !open;
    };
    wrap.addEventListener('click', toggle);
    wrap.addEventListener('keydown', (event) => {
      if (event.target !== wrap) return;
      if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault();
        toggle();
      }
    });
    body.addEventListener('click', (event) => event.stopPropagation());
    wrap.append(label, body);
    return wrap;
  }
  const wrap = document.createElement('div');
  wrap.className = 'task-monitor-entry task-monitor-status-line';
  wrap.dataset.entryKind = 'status';
  wrap.textContent = entry.label;
  return wrap;
}

function toolMonitorSection(label: string, payload: string, status = ''): HTMLElement {
  const section = document.createElement('section');
  section.className = 'task-monitor-tool-section';
  if (status) section.dataset.status = status;
  const heading = document.createElement('span');
  heading.textContent = label;
  const pre = document.createElement('pre');
  const code = document.createElement('code');
  let formatted = payload.trim();
  try {
    formatted = JSON.stringify(JSON.parse(formatted), null, 2);
  } catch {
    // Commands and plain-text responses should remain byte-for-byte readable.
  }
  code.textContent = formatted;
  pre.append(code);
  section.append(heading, pre);
  return section;
}

function emptyState(message: string): HTMLElement {
  const el = document.createElement('div');
  el.className = 'task-monitor-empty';
  el.textContent = message;
  return el;
}
