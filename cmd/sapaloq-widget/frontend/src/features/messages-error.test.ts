import { describe, it, expect, vi, beforeEach } from 'vitest';

const retryMock = vi.fn();
vi.mock('./message-actions', () => ({
  copyText: vi.fn(),
  deleteTurn: vi.fn(),
  editText: vi.fn(),
  retryTurn: (...args: unknown[]) => retryMock(...args),
}));

import { wireErrorMessage } from './messages';
import { patchTranscriptEntry } from '../ui/transcript/render';

describe('error retry button', () => {
  beforeEach(() => {
    retryMock.mockReset();
    document.body.innerHTML = '';
  });

  it('retries the preceding user turn, not the error turn id', () => {
    document.body.innerHTML = `
      <div id="message-list">
        <div class="transcript-pane">
          <div class="transcript-user message message--user" data-turn-id="42">user ask</div>
          <div class="transcript-error message message--error" data-turn-id="99">boom</div>
        </div>
      </div>
    `;
    const error = document.querySelector('.transcript-error') as HTMLElement;
    wireErrorMessage(error);
    error.querySelector('button')!.click();
    expect(retryMock).toHaveBeenCalledWith(42);
  });

  it('keeps the retry button when transcript error text is patched', () => {
    const el = document.createElement('div');
    el.className = 'transcript-error message message--error';
    el.dataset.entryKind = 'error';
    const body = document.createElement('div');
    body.className = 'transcript-entry-body';
    body.textContent = 'first';
    el.append(body);
    wireErrorMessage(el);

    patchTranscriptEntry(el, { kind: 'error', text: 'updated error' });
    const btn = el.querySelector('.message-inline-actions button') as HTMLButtonElement;
    expect(btn).not.toBeNull();
    expect(el.querySelector('.transcript-entry-body')?.textContent).toContain('updated error');
  });
});
