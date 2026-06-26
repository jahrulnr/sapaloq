// Streaming + progress UI: the per-turn stream coalescer (thinking/assistant
// bubbles), the live progress bubble + countdown, and the asynchronous
// background sub-agent task cards.
import type { StreamEvent, StreamTarget, StreamRenderer } from '../core/types';
import { getMessageList, hasVisibleText, scrollMessagesToBottom } from '../ui/dom';
import { renderMarkdown } from '../ui/markdown';
import { appendMessage, wireAssistantFeedback } from './messages';
import { setRingState } from './connection';
import { refreshRuntimeStatus } from './runtime-status';
import { getUserGroup, nextMessageSeq, taskBubbles, taskStatuses } from '../core/state';

// ---------------------------------------------------------------------------
// Progress bubble
// ---------------------------------------------------------------------------

let activeProgressBubble: HTMLElement | null = null;
let activeCountdown: ReturnType<typeof setInterval> | null = null;

function stopCountdown() {
  if (activeCountdown !== null) {
    clearInterval(activeCountdown);
    activeCountdown = null;
  }
}

export function appendProgressBubble(
  label: 'waiting' | 'thinking' | 'working' | 'compacting' | 'stopping',
  seconds = 0,
) {
  stopCountdown();
  activeProgressBubble?.remove();
  const list = getMessageList();
  if (!list) return null;
  const bubble = document.createElement('div');
  bubble.className = 'message message--status';
  bubble.dataset.status = label;
  bubble.innerHTML = `<span class="status-pulse" aria-hidden="true"><i></i><i></i><i></i></span><span class="status-label">${label}</span><span class="status-count" aria-live="polite"></span>`;
  list.append(bubble);
  list.scrollTop = list.scrollHeight;
  activeProgressBubble = bubble;

  // Live countdown so the user can see the wait is real (e.g. 10s, 9s, ...),
  // not a stalled turn. Driven purely client-side from the backend's window.
  if (label === 'waiting' && seconds > 0) {
    const countEl = bubble.querySelector('.status-count') as HTMLElement | null;
    let remaining = Math.floor(seconds);
    const paint = () => {
      if (countEl) countEl.textContent = remaining > 0 ? `· ${remaining}s` : '· 0s';
    };
    paint();
    activeCountdown = setInterval(() => {
      remaining -= 1;
      if (remaining <= 0) {
        remaining = 0;
        paint();
        stopCountdown();
        return;
      }
      paint();
    }, 1000);
  }
  return bubble;
}

export function clearProgressBubble() {
  stopCountdown();
  activeProgressBubble?.remove();
  activeProgressBubble = null;
}

// ---------------------------------------------------------------------------
// Stream coalescer
// ---------------------------------------------------------------------------

function makeStreamTarget(className: string): StreamTarget {
  const list = getMessageList();
  const el = document.createElement('div');
  el.className = `message ${className} message--streaming`;
  el.dataset.seq = `${nextMessageSeq()}`;
  el.dataset.group = `${getUserGroup()}`;
  if (list) list.appendChild(el);
  scrollMessagesToBottom();
  return { el, text: '', queue: '', typing: false };
}

// makeThinkingTarget builds a collapsible reasoning bubble: a clickable header
// (with a chevron) plus a body the deltas stream into. The bubble is never
// hidden - only toggled - so finished reasoning stays available for review.
function makeThinkingTarget(): StreamTarget {
  const list = getMessageList();
  const el = document.createElement('div');
  el.className = 'message message--thinking message--streaming is-expanded';
  el.dataset.seq = `${nextMessageSeq()}`;
  el.dataset.group = `${getUserGroup()}`;

  const header = document.createElement('button');
  header.type = 'button';
  header.className = 'thinking-toggle';
  header.innerHTML = `<span class="status-pulse" aria-hidden="true"><i></i><i></i><i></i></span><span class="thinking-label">thinking</span><span class="thinking-chevron" aria-hidden="true">⌄</span>`;

  const body = document.createElement('div');
  body.className = 'thinking-body';

  header.addEventListener('click', (event) => {
    event.stopPropagation();
    const expanded = el.classList.toggle('is-expanded');
    el.classList.toggle('is-collapsed', !expanded);
    header.setAttribute('aria-expanded', String(expanded));
  });
  header.setAttribute('aria-expanded', 'true');

  el.append(header, body);
  if (list) list.appendChild(el);
  scrollMessagesToBottom();
  return { el, body, text: '', queue: '', typing: false };
}

function paintStream(target: StreamTarget) {
  const sink = target.body || target.el;
  sink.replaceChildren(renderMarkdown(target.text));
  scrollMessagesToBottom();
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
  // Reasoning bubbles auto-collapse once complete so the answer that follows is
  // front-and-centre, but the toggle keeps them re-expandable. Mark the bubble
  // as done so the header shows a finished state (no pulse, "thought") instead
  // of looking identical to a still-streaming reasoning bubble.
  if (target.body && target.el.classList.contains('message--thinking')) {
    target.el.classList.remove('is-expanded');
    target.el.classList.add('is-collapsed', 'is-done');
    const toggle = target.el.querySelector('.thinking-toggle');
    toggle?.setAttribute('aria-expanded', 'false');
    const label = target.el.querySelector('.thinking-label');
    if (label) label.textContent = 'thought';
  } else if (target.el.classList.contains('message--assistant')) {
    // A settled assistant bubble with no visible rendered text (e.g. the model
    // emitted only whitespace, or content that rendered to nothing) is dropped
    // entirely - we never show a blank bubble nor attach feedback to it.
    if (!hasVisibleText(target.el)) {
      target.el.remove();
    } else if (!target.el.querySelector('.message-feedback')) {
      // Final assistant answer: attach 👍/👎 once the stream settles.
      wireAssistantFeedback(target.el);
    }
  }
}

function pushStream(target: StreamTarget, chunk: string) {
  if (!chunk) return;
  target.queue += chunk;
  if (!target.typing) {
    target.typing = true;
    typeNext(target);
  }
}

export function newStreamRenderer(): StreamRenderer {
  return { thinking: null, assistant: null };
}

export function feedStreamEvent(r: StreamRenderer, event: StreamEvent) {
  if (event.kind === 'thinking_delta') {
    setRingState('thinking');
    if (!r.thinking) r.thinking = makeThinkingTarget();
    pushStream(r.thinking, event.delta || '');
  } else if (event.kind === 'tool_call') {
    setRingState('delegating');
    if (r.thinking) { flushStream(r.thinking); r.thinking = null; }
    if (r.assistant) { flushStream(r.assistant); r.assistant = null; }
    appendMessage('message--tool', `tool: ${event.tool_call?.name || 'unknown'}`);
  } else if (event.kind === 'response_delta') {
    // Ignore empty/whitespace-only deltas so a stray empty chunk never spawns a
    // blank assistant bubble (which previously also got 👍/👎 controls).
    const delta = event.delta || '';
    if (!r.assistant && delta.trim() === '') return;
    clearProgressBubble();
    if (!r.assistant) r.assistant = makeStreamTarget('message--assistant');
    pushStream(r.assistant, delta);
  } else if (event.kind === 'status') {
    const status = event.status;
    if (status === 'waiting' || status === 'thinking' || status === 'working' || status === 'compacting' || status === 'stopping') {
      appendProgressBubble(status, status === 'waiting' ? event.wait_seconds || 0 : 0);
    }
  } else if (event.kind === 'turn_boundary') {
    // The run looped to a new inference turn (e.g. an <sapaloq:autopilot>
    // continuation). Flush the current turn's bubbles so the next turn's
    // narration lands in a FRESH bubble instead of merging into the previous
    // one. flushStream is null-safe and already drops an empty assistant
    // bubble + wires 👍/👎 on a settled one, so no extra guarding is needed.
    if (r.thinking) { flushStream(r.thinking); r.thinking = null; }
    if (r.assistant) { flushStream(r.assistant); r.assistant = null; }
  } else if (event.kind === 'error') {
    clearProgressBubble();
    if (r.thinking) { flushStream(r.thinking); r.thinking = null; }
    if (r.assistant) { flushStream(r.assistant); r.assistant = null; }
    appendMessage('message--error', event.error || 'chat failed');
    setRingState('idle');
  } else if (event.kind === 'done') {
    finishStreamRenderer(r);
    setRingState('idle');
  }
}

export function finishStreamRenderer(r: StreamRenderer) {
  clearProgressBubble();
  if (r.thinking) { flushStream(r.thinking); r.thinking = null; }
  if (r.assistant) { flushStream(r.assistant); r.assistant = null; }
}

// renderEvents replays a batch of events into a fresh renderer. Used as the
// fallback path (e.g. plain browser without the Wails runtime) when no live
// events were delivered.
export function renderEvents(events: StreamEvent[]) {
  const r = newStreamRenderer();
  for (const event of events) feedStreamEvent(r, event);
  finishStreamRenderer(r);
}

// ---------------------------------------------------------------------------
// Background task cards
// ---------------------------------------------------------------------------

// renderTaskUpdate surfaces asynchronous background sub-agent lifecycle
// updates as one concise card per task. Snapshot catch-up can overlap queued
// live events, so a stale active event must never regress an already-terminal
// card.
export function renderTaskUpdate(event: StreamEvent) {
  const status = event.task_status || '';
  const taskID = event.task_id || '';
  const previousStatus = taskID ? taskStatuses.get(taskID) : undefined;
  const terminal = new Set(['done', 'failed', 'stopped']);
  if (previousStatus && terminal.has(previousStatus) && !terminal.has(status)) return;
  const role = event.task_role || 'task';

  // Record the latest status and recompute the orb's busy ring FIRST, before any
  // early return. A terminal transition (done/failed/stopped) must always clear
  // the busy flag - even if its summary happens to be empty - otherwise the orb
  // stays stuck pulsing ("responding") after the sub-agent has finished. The
  // bubble rendering below needs a summary; the ring state must not depend on it.
  if (taskID) taskStatuses.set(taskID, status);
  if (status === 'pending' || status === 'in_progress') {
    setRingState('delegating');
  } else if (status === 'awaiting_clarification') {
    setRingState('needs-input');
  } else {
    const active = [...taskStatuses.values()].some((value) => value === 'pending' || value === 'in_progress' || value === 'stopping');
    if (!active) setRingState('idle');
    // A terminal transition (done/failed/stopped) means the sub-agent has wound
    // down. Refresh the runtime-status pill immediately instead of waiting up to
    // 3s for the next poll - otherwise the "Agent" pill keeps blinking
    // "finalizing" after the task is already finished.
    if (terminal.has(status)) void refreshRuntimeStatus();
  }

  // The card is a STATUS TIMELINE, not a result dump. It shows a one-line
  // status label, plus a short activity hint while the task is running (e.g.
  // "Menjalankan `exec`."). The full, human-readable summary is authored by the
  // orchestrator and shown as its own chat bubble (response_delta tagged with
  // task_id) - rendering record.Result here too produced two identical,
  // redundant summaries. So for terminal states we deliberately drop the
  // summary body and keep only the status line.
  let prefix = '';
  switch (status) {
    case 'done': prefix = `✅ ${role} selesai`; break;
    case 'failed': prefix = `⚠️ ${role} gagal`; break;
    case 'awaiting_clarification': prefix = `❓ ${role} butuh keputusan`; break;
    case 'pending': prefix = `🕓 ${role} dijadwalkan`; break;
    case 'in_progress': prefix = `⏳ ${role} sedang bekerja`; break;
    case 'stopping': prefix = `⏹️ ${role} sedang dihentikan`; break;
    case 'stopped': prefix = `⏹️ ${role} dihentikan`; break;
    default: prefix = `${role}`; break;
  }

  const summary = (event.summary || '').trim();
  const isTerminal = terminal.has(status) || status === 'awaiting_clarification';
  // A short activity hint is useful WHILE running; a terminal card needs only
  // its status line (the summary lives in the orchestrator's bubble). The
  // failed/clarification status already carry their reason in `prefix`-adjacent
  // summary, so keep a one-liner there; done/stopped show status only.
  let activity = '';
  if (!isTerminal && summary && summary.length <= 120) {
    activity = summary;
  } else if (status === 'failed' || status === 'awaiting_clarification') {
    activity = summary && summary.length <= 200 ? summary : '';
  }
  const text = activity ? `**${prefix}**\n\n${activity}` : `**${prefix}**`;
  let item = taskID ? taskBubbles.get(taskID) : undefined;
  if (!item || !item.isConnected) {
    item = appendMessage('message--task', text);
    if (!item) return;
    if (taskID) {
      item.dataset.taskId = taskID;
      taskBubbles.set(taskID, item);
    }
  } else {
    item.dataset.rawText = text;
    item.replaceChildren(renderMarkdown(text));
    scrollMessagesToBottom();
  }
  // Status + ring state were already updated at the top of this function so a
  // terminal transition clears the busy ring regardless of summary presence.
}
