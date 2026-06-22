import { describe, it, expect } from 'vitest';
import { actorState } from './runtime-status';
import type { ActorRuntimeStatus } from '../core/types';

function actor(partial: Partial<ActorRuntimeStatus>): ActorRuntimeStatus {
  return {
    id: 'task-1',
    role: 'task-runner',
    status: 'in_progress',
    phase: 'working',
    workspace: '/tmp/profile',
    ...partial,
  } as ActorRuntimeStatus;
}

describe('actorState', () => {
  it('is idle when there is no actor', () => {
    expect(actorState(undefined)).toBe('idle');
  });

  it('is active while genuinely working', () => {
    expect(actorState(actor({ status: 'in_progress', phase: 'working' }))).toBe('active');
  });

  // Regression: a finished task left the pill blinking "finalizing" forever
  // because the only non-active states were failed/stopped. A settled worker
  // (done, or wound-down phase) must read as idle, not active.
  it('is idle when the task is done', () => {
    expect(actorState(actor({ status: 'done', phase: 'exited' }))).toBe('idle');
  });

  it('is idle when phase is finalizing even if status still says in_progress', () => {
    expect(actorState(actor({ status: 'in_progress', phase: 'finalizing' }))).toBe('idle');
  });

  it('is idle when phase is exited', () => {
    expect(actorState(actor({ status: 'in_progress', phase: 'exited' }))).toBe('idle');
  });

  it('reflects terminal failure / stop states', () => {
    expect(actorState(actor({ status: 'failed' }))).toBe('failed');
    expect(actorState(actor({ status: 'stopped' }))).toBe('stopped');
  });

  it('is case-insensitive for settled detection', () => {
    expect(actorState(actor({ status: 'DONE', phase: 'Finalizing' }))).toBe('idle');
  });
});
