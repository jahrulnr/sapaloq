import { describe, it, expect, vi, beforeEach } from 'vitest';

const restoreChatHistory = vi.fn().mockResolvedValue(true);

vi.mock('./history', () => ({
  restoreChatHistory: (...args: unknown[]) => restoreChatHistory(...args),
}));

import { spokenTaskIDs } from '../core/state';
import { applySpokenTaskCompletion } from './chat-controller';

describe('applySpokenTaskCompletion', () => {
  beforeEach(() => {
    spokenTaskIDs.clear();
    restoreChatHistory.mockClear();
  });

  it('restores chat history once per task_id completion delta', () => {
    const event = {
      kind: 'response_delta',
      task_id: 'task-42',
      delta: 'Agent sudah selesai bikin theme.',
    };
    expect(applySpokenTaskCompletion(event)).toBe(true);
    expect(restoreChatHistory).toHaveBeenCalledTimes(1);
    expect(applySpokenTaskCompletion(event)).toBe(false);
    expect(restoreChatHistory).toHaveBeenCalledTimes(1);
  });

  it('ignores live chat deltas without task_id', () => {
    expect(applySpokenTaskCompletion({ kind: 'response_delta', delta: 'hello' })).toBe(false);
    expect(restoreChatHistory).not.toHaveBeenCalled();
  });
});
