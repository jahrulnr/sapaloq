// Connection / ring-state indicators and the core ping loop. Owns the orb ring
// state, the connection dot, and the context-usage pill.
//
// Idle standby (overlay open, no chat transaction): ping/pong is the ONLY
// periodic IPC health probe. runtime_status and context_usage piggyback on
// successful pings when the panel is expanded — no parallel timers that can
// stack socket reads while the core is busy with background agents.
import { PingCore, ContextUsage } from '../../wailsjs/go/main/App';
import type { ConnectionState, RingState, ChatUsage } from '../core/types';
import { formatTokens } from '../ui/dom';
import { isExpanded } from '../ui/window-layout';
import { refreshRuntimeStatus } from './runtime-status';
import {
  getConnection,
  isSubmitting,
  setConnectionState,
} from '../core/state';

const states: RingState[] = ['idle', 'thinking', 'delegating', 'needs-input'];
let stateIndex = 0;
let pingTimer: ReturnType<typeof setInterval> | null = null;

export function setRingState(next: RingState) {
  const orb = document.getElementById('orb');
  if (orb) orb.dataset.state = next;
}

export function setConnection(state: ConnectionState) {
  setConnectionState(state);
  const dot = document.getElementById('conn-dot');
  if (dot) dot.dataset.state = state;
}

export function renderUsage(usage?: ChatUsage | null) {
  const el = document.getElementById('context-usage');
  if (!el || !usage) return;
  const text = `${formatTokens(usage.used_tokens)}/${formatTokens(usage.context_window)}`;
  el.textContent = text;
  let title = `${usage.percent}% context used · ${usage.provider || 'provider'} ${usage.model || ''}`.trim();
  if (usage.compacted_turns && usage.compacted_turns > 0) {
    title += ` · ${usage.active_turns ?? 0} active turns (${usage.compacted_turns} compacted)`;
  }
  el.title = title;
  el.dataset.level = usage.percent >= 80 ? 'danger' : usage.percent >= 70 ? 'warn' : 'normal';
}

export async function refreshUsage() {
  try {
    renderUsage((await ContextUsage()) as ChatUsage);
  } catch {
    // ping loop owns offline state
  }
}

export function cycleRingState() {
  stateIndex = (stateIndex + 1) % states.length;
  setRingState(states[stateIndex]);
}

let pingIntervalMs = 4000;
let pingFailures = 0;
let pingCount = 0;
let pingInFlight = false;
let statusInFlight = false;
let usageInFlight = false;

/** Piggyback secondary polls on ping so idle standby never opens parallel IPC sockets. */
function piggybackOnPing(wasOffline: boolean) {
  const idle = !isSubmitting();
  if (wasOffline) {
    void runSecondaryAfterPing(true);
    return;
  }
  if (!isExpanded()) return;
  pingCount++;
  if (idle) {
    void runSecondaryAfterPing(false);
    return;
  }
  if (pingCount % 6 === 0) maybeRefreshStatus();
}

/** Run at most one secondary IPC (status or usage) per successful ping. */
async function runSecondaryAfterPing(both: boolean) {
  if (both) {
    await maybeRefreshStatus();
    await maybeRefreshUsage();
    return;
  }
  // Alternate so idle never stacks runtime_status + context_usage on one tick.
  if (pingCount % 2 === 0) await maybeRefreshStatus();
  else await maybeRefreshUsage();
}

function maybeRefreshStatus(): Promise<void> {
  if (statusInFlight || !isExpanded()) return Promise.resolve();
  statusInFlight = true;
  return refreshRuntimeStatus().finally(() => { statusInFlight = false; });
}

function maybeRefreshUsage(): Promise<void> {
  if (usageInFlight || !isExpanded()) return Promise.resolve();
  usageInFlight = true;
  return refreshUsage().finally(() => { usageInFlight = false; });
}

export async function runPing() {
  if (pingInFlight) return;
  pingInFlight = true;
  try {
    const res = await PingCore();
    const wasOffline = getConnection() !== 'connected';
    resetPingBackoff();
    setConnection('connected');
    if (res.ring_state) setRingState(res.ring_state as RingState);
    piggybackOnPing(wasOffline);
  } catch {
    schedulePingBackoff();
    setConnection(getConnection() === 'connected' ? 'reconnecting' : 'disconnected');
  } finally {
    pingInFlight = false;
  }
}

export function startPingLoop() {
  if (pingTimer) return;
  void runPing();
  pingTimer = setInterval(() => void runPing(), pingIntervalMs);
}

function schedulePingBackoff() {
  pingFailures = Math.min(pingFailures + 1, 5);
  const next = Math.min(4000 * (1 << pingFailures), 60000);
  if (pingTimer) clearInterval(pingTimer);
  pingTimer = setInterval(() => void runPing(), next);
  pingIntervalMs = next;
}

function resetPingBackoff() {
  if (pingFailures === 0 && pingIntervalMs === 4000) return;
  pingFailures = 0;
  pingIntervalMs = 4000;
  if (pingTimer) clearInterval(pingTimer);
  pingTimer = setInterval(() => void runPing(), pingIntervalMs);
}
