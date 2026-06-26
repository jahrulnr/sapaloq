import { describe, it, expect, vi, beforeEach } from 'vitest';

// stream.ts pulls in connection + runtime-status, which import the Wails
// runtime bindings. None of that matters for the bubble-flushing behaviour
// under test, so stub the side-effecting modules.
vi.mock('./connection', () => ({ setRingState: vi.fn() }));
vi.mock('./runtime-status', () => ({ refreshRuntimeStatus: vi.fn() }));

import { feedStreamEvent, newStreamRenderer } from './stream';
import type { StreamEvent } from '../core/types';

function assistantBubbles() {
  return Array.from(document.querySelectorAll('.message--assistant'));
}

function delta(text: string): StreamEvent {
  return { kind: 'response_delta', delta: text };
}

describe('turn_boundary splits autopilot turns into separate bubbles', () => {
  beforeEach(() => {
    document.body.innerHTML = '<div id="message-list"></div>';
  });

  it('starts a fresh assistant bubble after a turn_boundary', () => {
    const r = newStreamRenderer();

    // Turn 1 narration.
    feedStreamEvent(r, delta('turn one narration. Aku tutup giliran ini.'));
    expect(assistantBubbles()).toHaveLength(1);

    // The run loops to a new inference turn.
    feedStreamEvent(r, { kind: 'turn_boundary' });
    // The first bubble has settled (no longer streaming) and the renderer no
    // longer holds an active assistant target.
    expect(r.assistant).toBeNull();
    const first = assistantBubbles()[0];
    expect(first.classList.contains('message--streaming')).toBe(false);

    // Turn 2 narration must land in a brand-new bubble, not merge into turn 1.
    // A trailing `done` flushes the typewriter queue so the full text is
    // painted synchronously for the assertion.
    feedStreamEvent(r, delta('turn two narration.'));
    feedStreamEvent(r, { kind: 'done' });
    const bubbles = assistantBubbles();
    expect(bubbles).toHaveLength(2);
    expect(bubbles[0].textContent).toContain('turn one');
    expect(bubbles[1].textContent).toContain('turn two');
    expect(bubbles[0].textContent).not.toContain('turn two');
  });

  it('drops an empty turn so a boundary after no text adds no blank bubble', () => {
    const r = newStreamRenderer();
    // A boundary with no preceding text must not create or leave a blank bubble.
    feedStreamEvent(r, { kind: 'turn_boundary' });
    expect(assistantBubbles()).toHaveLength(0);

    feedStreamEvent(r, delta('only real turn.'));
    feedStreamEvent(r, { kind: 'done' });
    const bubbles = assistantBubbles();
    expect(bubbles).toHaveLength(1);
    expect(bubbles[0].textContent).toContain('only real turn');
  });
});
