import { describe, it, expect, beforeEach } from 'vitest';
import { getUserGroup, setUserGroup } from '../core/state';
import { removeRepliesAfterTurn } from './history';

describe('removeRepliesAfterTurn', () => {
  beforeEach(() => {
    setUserGroup(1);
    document.body.innerHTML = `
      <div id="message-list">
        <div class="transcript-pane">
          <div class="transcript-user message message--user" data-turn-id="10" data-group="1">ask</div>
          <div class="tool-activity message" data-entry-kind="tool">exec</div>
          <div class="tool-activity message" data-entry-kind="tool">stop</div>
          <div class="transcript-error message message--error">boom</div>
        </div>
      </div>
    `;
  });

  it('removes tool rows and errors after the retried user turn', () => {
    removeRepliesAfterTurn(10);
    const pane = document.querySelector('.transcript-pane')!;
    expect(pane.children.length).toBe(1);
    expect(pane.querySelector('.transcript-user')?.textContent).toBe('ask');
    expect(pane.querySelector('.tool-activity')).toBeNull();
    expect(getUserGroup()).toBe(1);
  });
});
