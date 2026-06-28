import { beforeEach, describe, expect, it, vi } from 'vitest';

const chatHistoryMock = vi.hoisted(() => vi.fn());

vi.mock('../../wailsjs/go/main/App', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../wailsjs/go/main/App')>()),
  ChatHistory: (...args: unknown[]) => chatHistoryMock(...args),
}));

vi.mock('./connection', () => ({
  renderUsage: vi.fn(),
  setRingState: vi.fn(),
}));

vi.mock('./runtime-status', () => ({
  refreshRuntimeStatus: vi.fn(async () => undefined),
}));

import { captureMessageScroll } from '../ui/dom';
import { restoreChatHistory } from './history';
import { appendMessage } from './messages';
import {
  flushScheduledChatTranscript,
  mountChatTranscript,
  resetChatTranscriptState,
  scheduleSyncChatTranscript,
  syncChatTranscript,
} from './transcript-pane';

function messageList(): HTMLElement {
  return document.getElementById('message-list') as HTMLElement;
}

function installTranscriptGeometry(list: HTMLElement): void {
  Object.defineProperty(list, 'clientHeight', { value: 400, configurable: true });
  Object.defineProperty(list, 'scrollHeight', {
    configurable: true,
    get: () => 800 + (list.querySelector('.transcript-pane')?.children.length || 0) * 200,
  });
}

function installDirectMessageGeometry(list: HTMLElement): void {
  Object.defineProperty(list, 'clientHeight', { value: 400, configurable: true });
  Object.defineProperty(list, 'scrollHeight', {
    configurable: true,
    get: () => 800 + list.children.length * 200,
  });
}

describe('chat transcript scroll follow', () => {
  beforeEach(() => {
    chatHistoryMock.mockReset();
    flushScheduledChatTranscript();
    resetChatTranscriptState();
    document.body.innerHTML = '<div id="message-list"></div>';
  });

  it('follows streaming content when the reader is exactly at the bottom', () => {
    mountChatTranscript([{ kind: 'text', text: 'one' }]);
    const list = messageList();
    installTranscriptGeometry(list);
    list.scrollTop = list.scrollHeight - list.clientHeight;

    syncChatTranscript([
      { kind: 'text', text: 'one' },
      { kind: 'text', text: 'two' },
    ]);

    expect(list.scrollTop).toBe(list.scrollHeight);
  });

  it('keeps the reading position when the reader has moved up', () => {
    mountChatTranscript([{ kind: 'text', text: 'one' }]);
    const list = messageList();
    installTranscriptGeometry(list);
    list.scrollTop = 100;

    syncChatTranscript([
      { kind: 'text', text: 'one' },
      { kind: 'text', text: 'two' },
    ]);

    expect(list.scrollTop).toBe(100);
  });

  it('resumes following after the reader returns to the bottom', () => {
    mountChatTranscript([{ kind: 'text', text: 'one' }]);
    const list = messageList();
    installTranscriptGeometry(list);
    list.scrollTop = 100;
    syncChatTranscript([
      { kind: 'text', text: 'one' },
      { kind: 'text', text: 'two' },
    ]);
    expect(list.scrollTop).toBe(100);

    list.scrollTop = list.scrollHeight - list.clientHeight;
    syncChatTranscript([
      { kind: 'text', text: 'one' },
      { kind: 'text', text: 'two' },
      { kind: 'text', text: 'three' },
    ]);

    expect(list.scrollTop).toBe(list.scrollHeight);
  });

  it('uses the latest reader position when a scheduled streaming patch flushes', () => {
    mountChatTranscript([{ kind: 'text', text: 'one' }]);
    const list = messageList();
    installTranscriptGeometry(list);
    list.scrollTop = list.scrollHeight - list.clientHeight;

    scheduleSyncChatTranscript([
      { kind: 'text', text: 'one' },
      { kind: 'text', text: 'two' },
    ]);
    list.scrollTop = 100;
    flushScheduledChatTranscript();

    expect(list.scrollTop).toBe(100);
  });

  it('preserves an away-from-bottom position across a full transcript remount', () => {
    mountChatTranscript([
      { kind: 'text', text: 'one' },
      { kind: 'text', text: 'two' },
    ]);
    const list = messageList();
    installTranscriptGeometry(list);
    list.scrollTop = 100;
    const scroll = captureMessageScroll(list);

    list.innerHTML = '';
    resetChatTranscriptState();
    mountChatTranscript([
      { kind: 'text', text: 'one' },
      { kind: 'text', text: 'two' },
      { kind: 'text', text: 'three' },
    ], scroll);

    expect(list.scrollTop).toBe(100);
  });

  it('preserves the reading position through an actual same-session history restore', async () => {
    mountChatTranscript([
      { kind: 'text', text: 'one' },
      { kind: 'text', text: 'two' },
    ]);
    const list = messageList();
    installTranscriptGeometry(list);
    list.scrollTop = 100;
    chatHistoryMock.mockResolvedValue({
      session_id: 'chat-1',
      transcript: [
        { kind: 'text', text: 'one' },
        { kind: 'text', text: 'two' },
        { kind: 'text', text: 'three' },
      ],
      usage: { used_tokens: 1, context_window: 100, percent: 1 },
    });

    await restoreChatHistory();

    expect(list.scrollTop).toBe(100);
  });

  it('opens an explicit session navigation at the newest entry', async () => {
    mountChatTranscript([
      { kind: 'text', text: 'one' },
      { kind: 'text', text: 'two' },
    ]);
    const list = messageList();
    installTranscriptGeometry(list);
    list.scrollTop = 100;
    chatHistoryMock.mockResolvedValue({
      session_id: 'chat-2',
      transcript: [
        { kind: 'text', text: 'one' },
        { kind: 'text', text: 'two' },
        { kind: 'text', text: 'three' },
      ],
      usage: { used_tokens: 1, context_window: 100, percent: 1 },
    });

    await restoreChatHistory(true);

    expect(list.scrollTop).toBe(list.scrollHeight);
  });

  it('uses only a rounding tolerance at the end of the transcript', () => {
    const list = messageList();
    Object.defineProperty(list, 'clientHeight', { value: 400, configurable: true });
    Object.defineProperty(list, 'scrollHeight', { value: 1000, configurable: true });

    list.scrollTop = 598;
    expect(captureMessageScroll(list).atBottom).toBe(true);
    list.scrollTop = 597;
    expect(captureMessageScroll(list).atBottom).toBe(false);
  });

  it('treats a short non-overflowing transcript as being at the bottom', () => {
    const list = messageList();
    Object.defineProperty(list, 'clientHeight', { value: 400, configurable: true });
    Object.defineProperty(list, 'scrollHeight', { value: 300, configurable: true });

    expect(captureMessageScroll(list).atBottom).toBe(true);
  });

  it('applies the same rule to directly appended chat bubbles', () => {
    const list = messageList();
    installDirectMessageGeometry(list);
    list.scrollTop = 100;
    appendMessage('message--assistant', 'first');
    expect(list.scrollTop).toBe(100);

    list.scrollTop = list.scrollHeight - list.clientHeight;
    appendMessage('message--assistant', 'second');
    expect(list.scrollTop).toBe(list.scrollHeight);
  });
});
