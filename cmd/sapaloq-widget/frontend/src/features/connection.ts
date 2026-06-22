// Connection / ring-state indicators and the core ping loop. Owns the orb ring
// state, the connection dot, and the context-usage pill.
import { PingCore } from '../../wailsjs/go/main/App';
import type { ConnectionState, RingState, ChatUsage } from '../core/types';
import { formatTokens } from '../ui/dom';
import {
  getConnection,
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
  el.title = `${usage.percent}% context used · ${usage.provider || 'provider'} ${usage.model || ''}`.trim();
  el.dataset.level = usage.percent >= 80 ? 'danger' : usage.percent >= 70 ? 'warn' : 'normal';
}

export function cycleRingState() {
  stateIndex = (stateIndex + 1) % states.length;
  setRingState(states[stateIndex]);
}

export async function runPing() {
  try {
    const res = await PingCore();
    setConnection('connected');
    if (res.ring_state) setRingState(res.ring_state as RingState);
  } catch {
    setConnection(getConnection() === 'connected' ? 'reconnecting' : 'disconnected');
  }
}

export function startPingLoop() {
  if (pingTimer) return;
  void runPing();
  pingTimer = setInterval(() => void runPing(), 4000);
}
