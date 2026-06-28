import { beforeEach, describe, expect, it, vi } from 'vitest';

const sendMessage = vi.fn(async (_sessionID: string, _message: string) => ({}));
const steerChat = vi.fn(async (_sessionID: string, _message: string) => undefined);

vi.mock('../../wailsjs/go/main/App', () => ({
  DeleteChatTurn: vi.fn(),
  NotificationSound: vi.fn(async () => ''),
  NotificationSoundForRole: vi.fn(async () => ''),
  OpenAttachment: vi.fn(),
  OpenExternal: vi.fn(),
  RetryChatTurn: vi.fn(),
  SendMessage: (sessionID: string, message: string) => sendMessage(sessionID, message),
  SteerChat: (sessionID: string, message: string) => steerChat(sessionID, message),
  StopChat: vi.fn(),
  SubmitFeedback: vi.fn(),
}));

vi.mock('../../wailsjs/runtime/runtime', () => ({
  EventsOn: vi.fn(),
  SendNotification: vi.fn(),
}));

vi.mock('./connection', () => ({
  renderUsage: vi.fn(),
  runPing: vi.fn(async () => undefined),
  setConnection: vi.fn(),
  setRingState: vi.fn(),
}));

vi.mock('./history', () => ({
  bindLatestGroupTurnID: vi.fn(async () => undefined),
  loadSessionList: vi.fn(async () => undefined),
  removeRepliesAfterTurn: vi.fn(() => 0),
  restoreChatHistory: vi.fn(async () => true),
  scheduleRestoreChatHistory: vi.fn(),
}));

import { isSubmitting, setCompose, setSessionID, setSubmitting } from '../core/state';
import { ComposeBox } from '../ui/compose';
import { APP_TEMPLATE } from '../ui/template';
import { setSubmittingUI, submitMessage, submitSteering } from './chat-controller';

function setupCompose() {
  document.body.innerHTML = `<div id="app">${APP_TEMPLATE}</div>`;
  const input = document.getElementById('compose-input') as HTMLElement;
  const compose = new ComposeBox(input, {
    onSubmit: () => void (isSubmitting() ? submitSteering() : submitMessage()),
  });
  setCompose(compose);
  setSessionID('session-1');
  return { compose, input };
}

async function flush() {
  await Promise.resolve();
  await Promise.resolve();
}

describe('foreground chat steering', () => {
  beforeEach(() => {
    sendMessage.mockClear();
    steerChat.mockClear();
    setSubmitting(false);
    setCompose(null);
    document.body.innerHTML = '';
  });

  it('keeps compose enabled and routes Enter to Steer while running', async () => {
    const { compose, input } = setupCompose();
    setSubmitting(true);
    setSubmittingUI(true);
    compose.insertText('Use the JSON API instead.');

    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
    await flush();

    expect(steerChat).toHaveBeenCalledWith('session-1', 'Use the JSON API instead.');
    expect(sendMessage).not.toHaveBeenCalled();
    expect(input.getAttribute('contenteditable')).toBe('true');
    expect(input.classList.contains('is-disabled')).toBe(false);
    expect(document.getElementById('steer-btn')?.hasAttribute('hidden')).toBe(false);
    expect(document.getElementById('stop-btn')?.hasAttribute('hidden')).toBe(false);
    expect(document.getElementById('send-btn')?.hasAttribute('hidden')).toBe(true);
    expect(document.querySelector('.message--steering')?.textContent).toContain('Use the JSON API instead.');
    expect(compose.isEmpty()).toBe(true);
  });

  it('keeps Shift+Enter as a newline gesture without steering', async () => {
    const { compose, input } = setupCompose();
    setSubmitting(true);
    setSubmittingUI(true);
    compose.insertText('first line');

    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', shiftKey: true, bubbles: true, cancelable: true }));
    await flush();

    expect(steerChat).not.toHaveBeenCalled();
    expect(sendMessage).not.toHaveBeenCalled();
  });

  it('routes Enter to normal Send while idle', async () => {
    const { compose, input } = setupCompose();
    compose.insertText('Start a new turn.');

    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
    await flush();

    expect(sendMessage).toHaveBeenCalledWith('session-1', 'Start a new turn.');
    expect(steerChat).not.toHaveBeenCalled();
  });

  it('rejects attachments without clearing the steering draft', async () => {
    const { compose, input } = setupCompose();
    setSubmitting(true);
    setSubmittingUI(true);
    compose.insertText('Inspect this ');
    compose.insertAttachment({ name: 'trace.txt', type: 'text/plain', size: 5, text: 'trace' });

    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
    await flush();

    expect(steerChat).not.toHaveBeenCalled();
    expect(compose.isEmpty()).toBe(false);
    expect(document.getElementById('steering-hint')?.textContent).toContain('hanya mendukung teks');
  });

  it('keeps the draft and marks the optimistic bubble when queueing fails', async () => {
    steerChat.mockRejectedValueOnce(new Error('generation already idle'));
    const { compose, input } = setupCompose();
    setSubmitting(true);
    setSubmittingUI(true);
    compose.insertText('Change the output format.');

    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
    await flush();

    expect(compose.isEmpty()).toBe(false);
    expect(document.querySelector('.message--steering')?.classList.contains('is-failed')).toBe(true);
    expect(document.getElementById('steering-hint')?.textContent).toBe('generation already idle');
  });
});
