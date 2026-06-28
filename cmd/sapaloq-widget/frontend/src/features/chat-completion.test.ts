import { describe, it, expect, vi, beforeEach } from 'vitest';

const restoreChatHistory = vi.fn().mockResolvedValue(true);
const scheduleRestoreChatHistory = vi.fn();

vi.mock('./history', () => ({
  restoreChatHistory: (...args: unknown[]) => restoreChatHistory(...args),
  scheduleRestoreChatHistory: (...args: unknown[]) => scheduleRestoreChatHistory(...args),
}));

import { spokenTaskIDs } from '../core/state';
import { applySpokenTaskCompletion } from './chat-controller';

describe('applySpokenTaskCompletion', () => {
  beforeEach(() => {
    spokenTaskIDs.clear();
    restoreChatHistory.mockClear();
    scheduleRestoreChatHistory.mockClear();
  });

  it('schedules chat history restore once per task_id completion delta', () => {
    const event = {
      kind: 'response_delta',
      task_id: 'task-42',
      delta: 'Agent sudah selesai bikin theme.',
    };
    expect(applySpokenTaskCompletion(event)).toBe(true);
    expect(scheduleRestoreChatHistory).toHaveBeenCalledTimes(1);
    expect(restoreChatHistory).not.toHaveBeenCalled();
    expect(applySpokenTaskCompletion(event)).toBe(false);
    expect(scheduleRestoreChatHistory).toHaveBeenCalledTimes(1);
  });

  it('ignores live chat deltas without task_id', () => {
    expect(applySpokenTaskCompletion({ kind: 'response_delta', delta: 'hello' })).toBe(false);
    expect(restoreChatHistory).not.toHaveBeenCalled();
  });
});
