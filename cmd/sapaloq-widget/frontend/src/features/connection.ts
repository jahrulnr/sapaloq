// Connection / ring-state indicators and the core ping loop. Owns the orb ring
// state, the connection dot, and the context-usage pill.
import { PingCore, ContextUsage } from '../../wailsjs/go/main/App';
import type { ConnectionState, RingState, ChatUsage } from '../core/types';
import { formatTokens } from '../ui/dom';
import { refreshRuntimeStatus } from './runtime-status';
import {
  getConnection,
  setConnectionState,
} from '../core/state';

const states: RingState[] = ['idle', 'thinking', 'delegating', 'needs-input'];
let stateIndex = 0;
let pingTimer: ReturnType<typeof setInterval> | null = null;
let usageTimer: ReturnType<typeof setInterval> | null = null;

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
  el.title = `${usage.percent}% context used · ${usage.provider || 'provider'} ${usage.model || ''}`.trim();
  el.dataset.level = usage.percent >= 80 ? 'danger' : usage.percent >= 70 ? 'warn' : 'normal';
}

// refreshUsage re-fetches the live context usage from core and re-renders the
// pill. Best-effort: a failure (core not ready / socket down) is swallowed so
// the ping loop stays the single owner of offline feedback. This is the fix
// for the "0/0 on open" bug: the one-shot startup ContextUsage() call could
// race the core service and fail, leaving the placeholder forever because
// nothing retried.
export async function refreshUsage() {
  try {
    renderUsage((await ContextUsage()) as ChatUsage);
  } catch {
    // ignore - ping loop owns offline state
  }
}

export function cycleRingState() {
  stateIndex = (stateIndex + 1) % states.length;
  setRingState(states[stateIndex]);
}

export async function runPing() {
  try {
    const res = await PingCore();
    const wasOffline = getConnection() !== 'connected';
    setConnection('connected');
    if (res.ring_state) setRingState(res.ring_state as RingState);
    // On (re)connect after being offline, the context pill may still hold a
    // stale/empty startup value (e.g. "0/0" when core wasn't ready yet). Pull
    // a fresh reading so the pill reflects reality as soon as core is up.
    if (wasOffline) {
      void refreshUsage();
      void refreshRuntimeStatus();
    }
  } catch {
    setConnection(getConnection() === 'connected' ? 'reconnecting' : 'disconnected');
  }
}

export function startPingLoop() {
  if (pingTimer) return;
  void runPing();
  pingTimer = setInterval(() => void runPing(), 4000);
  // Context usage changes as the conversation grows; refresh on a slower cadence
  // than ping so the pill stays accurate without hammering the store query.
  if (!usageTimer) {
    usageTimer = setInterval(() => void refreshUsage(), 15000);
  }
}
