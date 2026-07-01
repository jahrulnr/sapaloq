import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

vi.mock('../../wailsjs/go/main/App', () => ({
  ChatHistory: vi.fn().mockResolvedValue({ session_id: 's1', transcript: [] }),
  ContextUsage: vi.fn(),
  DeleteSession: vi.fn(),
  ListSessions: vi.fn(),
  NewSession: vi.fn(),
  SwitchSession: vi.fn(),
}));

vi.mock('./runtime-status', () => ({
  applyWorkspacePath: vi.fn(),
  refreshRuntimeStatus: vi.fn(),
}));

vi.mock('./messages', () => ({
  clearMessages: vi.fn(),
  clearToolActivityCache: vi.fn(),
}));

vi.mock('./transcript-pane', () => ({
  mountChatTranscript: vi.fn(),
  resetChatTranscriptState: vi.fn(),
}));

vi.mock('./connection', () => ({
  renderUsage: vi.fn(),
}));

vi.mock('../ui/dom', () => ({
  captureMessageScroll: vi.fn(() => ({ atBottom: true, scrollTop: 0 })),
  getMessageList: vi.fn(() => null),
}));

describe('scheduleRestoreChatHistory guard', () => {
  beforeEach(() => {
    vi.resetModules();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('does not schedule restore while submitting', async () => {
    vi.doMock('../core/state', () => ({
      getUserGroup: () => 0,
      getSessionID: () => 's1',
      setSessionID: vi.fn(),
      isSubmitting: () => true,
    }));
    const { scheduleRestoreChatHistory } = await import('./history');
    const { ChatHistory } = await import('../../wailsjs/go/main/App');
    scheduleRestoreChatHistory(10);
    await vi.advanceTimersByTimeAsync(20);
    expect(ChatHistory).not.toHaveBeenCalled();
  });
});
