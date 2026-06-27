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
  // Cached last inspect so a tab switch re-renders without a refetch.
  last: TaskInspectResult | null;
}

let overlay: HTMLDivElement | null = null;
let pollTimer: ReturnType<typeof setInterval> | null = null;
let activeTab: Tab = 'planner';
let activeSubTab: SubTab = 'activity';
let escapeHandler: ((e: KeyboardEvent) => void) | null = null;
const actorState: Record<Tab, ActorState> = {
  planner: { taskID: '', active: false, lastEventCount: 0, last: null },
  agent: { taskID: '', active: false, lastEventCount: 0, last: null },
};

function isSettled(actor: { status?: string; phase?: string } | undefined): boolean {
  if (!actor) return true;
  const status = (actor.status || '').toLowerCase();
  const phase = (actor.phase || '').toLowerCase();
  return status === 'done' || status === 'failed' || status === 'stopped' ||
    phase === 'finalizing' || phase === 'exited';
}

function roleLabel(role: Tab): string {
  return role === 'agent' ? 'Agent' : 'Planner';
}

// openTaskMonitor opens the pop-up. When opts.tab is provided, that tab is
// activated; otherwise the last active tab is kept. Safe to call when an
// overlay is already open (it just switches tabs).
export async function openTaskMonitor(opts?: { tab?: Tab }) {
  if (opts?.tab) activeTab = opts.tab;
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

  panel.append(header, tabs, subTabs, body);
  el.append(panel);
  document.body.append(el);
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
  void renderCurrent();
}

function switchSubTab(sub: SubTab) {
  activeSubTab = sub;
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
  actorState.planner.taskID = plannerActor?.id || '';
  actorState.planner.active = !!plannerActor && !isSettled(plannerActor);
  actorState.agent.taskID = agentActor?.id || '';
  actorState.agent.active = !!agentActor && !isSettled(agentActor);

  await fetchTab(activeTab);
  renderTabs();
  renderCurrent();
}

async function fetchTab(tab: Tab) {
  const state = actorState[tab];
  if (!state.taskID) {
    state.last = null;
    state.lastEventCount = 0;
    return;
  }
  try {
    const res = (await TaskInspect(state.taskID, state.lastEventCount)) as unknown as TaskInspectResult;
    state.last = res;
    state.lastEventCount = res.event_count ?? state.lastEventCount;
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
  body.replaceChildren();
  subTabs.replaceChildren();
  subTabs.hidden = true;

  const state = actorState[activeTab];
  const res = state.last;

  // Sub-tabs: Planner gets Activity | Plan; Agent gets Activity only (plus a
  // read-only Plan view when the agent is executing a handed-off plan).
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
    body.append(emptyState(`${roleLabel(activeTab)} tidak aktif`));
    return;
  }

  body.append(buildHeaderLine(res, state.active));
  if (activeSubTab === 'plan') {
    body.append(buildPlanPane(res));
  } else {
    body.append(buildActivityPane(res));
  }
}

function buildHeaderLine(res: TaskInspectResult, active: boolean): HTMLElement {
  const line = document.createElement('div');
  line.className = 'task-monitor-headline';
  const status = document.createElement('span');
  status.className = 'task-monitor-status';
  status.dataset.status = res.status || (active ? 'active' : 'idle');
  status.textContent = statusLabel(res.status, active);
  const task = document.createElement('span');
  task.className = 'task-monitor-task';
  task.textContent = res.task || '(no task text)';
  line.append(status, task);
  if (res.question) {
    const q = document.createElement('div');
    q.className = 'task-monitor-question';
    q.textContent = '❓ ' + res.question;
    const wrap = document.createElement('div');
    wrap.className = 'task-monitor-headline-wrap';
    wrap.append(line, q);
    return wrap;
  }
  return line;
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

function buildActivityPane(res: TaskInspectResult): HTMLElement {
  const pane = document.createElement('div');
  pane.className = 'task-monitor-activity';
  const events = res.events || [];
  if (events.length === 0) {
    pane.append(emptyState('Belum ada aktivitas'));
    return pane;
  }
  // Coalesce consecutive response_delta / thinking_delta chunks into single
  // entries so the stream reads as natural turns, not one node per token.
  const coalesced = coalesceEvents(events);
  for (const entry of coalesced) {
    pane.append(renderActivityEntry(entry));
  }
  return pane;
}

type ActivityEntry =
  | { kind: 'thinking'; text: string }
  | { kind: 'text'; text: string }
  | { kind: 'tool'; name: string; args: string }
  | { kind: 'status'; label: string }
  | { kind: 'turn'; label: string }
  | { kind: 'task'; label: string };

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
        out.push({ kind: 'tool', name: ev.tool_name || 'tool', args: ev.tool_arguments || '' });
        break;
      case 'turn_boundary':
        flushText();
        flushThinking();
        out.push({ kind: 'turn', label: '— turn boundary —' });
        break;
      case 'task_update':
        flushText();
        flushThinking();
        if (ev.summary) out.push({ kind: 'task', label: ev.summary });
        break;
      case 'status':
        // Skip noisy working/waiting heartbeats; surface only meaningful ones.
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
        // done/checkpoint/etc. are not part of the activity narrative.
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
    const wrap = document.createElement('div');
    wrap.className = 'task-monitor-entry task-monitor-tool';
    const label = document.createElement('span');
    label.className = 'task-monitor-entry-label';
    label.textContent = '🔧 ' + entry.name;
    const args = document.createElement('code');
    args.className = 'task-monitor-tool-args';
    args.textContent = truncateArgs(entry.args);
    wrap.append(label, args);
    return wrap;
  }
  if (entry.kind === 'turn') {
    const wrap = document.createElement('div');
    wrap.className = 'task-monitor-entry task-monitor-turn';
    wrap.textContent = entry.label;
    return wrap;
  }
  if (entry.kind === 'task') {
    const wrap = document.createElement('div');
    wrap.className = 'task-monitor-entry task-monitor-task-line';
    wrap.textContent = entry.label;
    return wrap;
  }
  // status
  const wrap = document.createElement('div');
  wrap.className = 'task-monitor-entry task-monitor-status-line';
  wrap.textContent = entry.label;
  return wrap;
}

function truncateArgs(args: string): string {
  const trimmed = args.trim();
  if (trimmed.length <= 160) return trimmed;
  return trimmed.slice(0, 160) + '…';
}

function emptyState(message: string): HTMLElement {
  const el = document.createElement('div');
  el.className = 'task-monitor-empty';
  el.textContent = message;
  return el;
}
