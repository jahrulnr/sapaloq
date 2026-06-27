import { describe, it, expect, vi, beforeEach } from 'vitest';

// stream.ts pulls in connection + runtime-status, which import the Wails
// runtime bindings. None of that matters for the bubble-flushing behaviour
// under test, so stub the side-effecting modules.
vi.mock('./connection', () => ({ setRingState: vi.fn() }));
vi.mock('./runtime-status', () => ({ refreshRuntimeStatus: vi.fn() }));

import { feedStreamEvent, newStreamRenderer } from './stream';
import { appendCheckpointDivider, appendMessage, appendSummaryPanel, appendThinkingBubble } from './messages';
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

  it('drops live thinking that renders only structural markdown', () => {
    const r = newStreamRenderer();
    feedStreamEvent(r, { kind: 'thinking_delta', delta: '---\n\n\u200b\n\n---' });
    feedStreamEvent(r, { kind: 'done' });
    expect(document.querySelector('.message--thinking')).toBeNull();
  });
});

describe('tool activity', () => {
  beforeEach(() => {
    document.body.innerHTML = '<div id="message-list"></div>';
  });

  it('pairs an expandable request with its eventual response', () => {
    const r = newStreamRenderer();
    feedStreamEvent(r, {
      kind: 'tool_call',
      tool_call: { id: 'call-1', name: 'exec', arguments: { command: 'make install' } },
    });
    // Durable progress can contain the same terminal event twice; the call ID
    // must keep history restore to one visible activity row.
    feedStreamEvent(r, {
      kind: 'tool_call',
      tool_call: { id: 'call-1', name: 'exec', arguments: { command: 'make install' } },
    });
    feedStreamEvent(r, {
      kind: 'tool_update',
      tool_call: { id: 'call-1', name: 'exec' },
      tool_result: 'installed',
      status: 'completed',
    });

    const activity = document.querySelector('.tool-activity') as HTMLElement;
    expect(document.querySelectorAll('.tool-activity')).toHaveLength(1);
    expect(activity.classList.contains('is-open')).toBe(true);
    expect(activity.querySelector<HTMLElement>('.tool-activity__body')?.hidden).toBe(false);
    expect(activity.dataset.complete).toBe('true');
    expect(activity.firstChild?.nodeType).toBe(3);
    expect(activity.firstChild?.textContent).toContain('$ exec');
    expect(activity.querySelector('button')).toBeNull();
    expect(activity.textContent).toContain('make install');
    expect(activity.textContent).toContain('installed');
    activity.click();
    expect(activity.classList.contains('is-open')).toBe(false);
    expect(activity.querySelector<HTMLElement>('.tool-activity__body')?.hidden).toBe(true);
  });

  it('shows a hint for empty exec output instead of vanishing on complete', () => {
    const r = newStreamRenderer();
    feedStreamEvent(r, {
      kind: 'tool_call',
      tool_call: { id: 'call-empty', name: 'exec', arguments: { command: 'mkdir -p /tmp/foo' } },
    });
    feedStreamEvent(r, {
      kind: 'tool_update',
      tool_call: { id: 'call-empty', name: 'exec' },
      tool_result: '',
      status: 'completed',
    });

    const activity = document.querySelector('.tool-activity') as HTMLElement;
    expect(activity.firstChild?.textContent).toContain('mkdir -p /tmp/foo');
    expect(activity.textContent).toContain('(no output)');
    expect(activity.classList.contains('is-open')).toBe(true);
  });

  it('keeps a root-level tool disclosure between consecutive thinking bubbles', () => {
    const r = newStreamRenderer();
    feedStreamEvent(r, { kind: 'thinking_delta', delta: 'Inspecting files' });
    feedStreamEvent(r, {
      kind: 'tool_call',
      tool_call: { id: 'call-between', name: 'read_file', arguments: { path: '/tmp/profile' } },
    });
    feedStreamEvent(r, { kind: 'thinking_delta', delta: 'Reviewing the result' });

    const list = document.getElementById('message-list')!;
    const activity = list.querySelector('.tool-activity') as HTMLElement;
    expect(activity).not.toBeNull();
    expect(activity.parentElement).toBe(list);
    expect(activity.firstChild?.nodeType).toBe(3);
    expect(activity.firstChild?.textContent).toContain('$ read_file');
    expect(activity.getAttribute('aria-expanded')).toBe('true');
  });
});

describe('empty restored assistant content', () => {
  beforeEach(() => {
    document.body.innerHTML = '<div id="message-list"></div>';
  });

  it('hides markdown that renders only separators', () => {
    const item = appendMessage('message--assistant', '---\n\n\u200b\n\n---\n\n---');
    expect(item).toBeUndefined();
    expect(document.querySelector('.message--assistant')).toBeNull();
    expect(document.querySelector('.message-feedback')).toBeNull();
  });

  it('keeps the checkpoint seam but hides an empty summary panel', () => {
    appendCheckpointDivider(1, '---\n\n\u200b\n\n---');
    expect(document.querySelector('.checkpoint-divider__label')?.textContent).toBe('Checkpoint 1');
    expect(document.querySelector('.summary-panel')).toBeNull();
  });

  it('uses a button-driven summary disclosure compatible with WebKitGTK', () => {
    const panel = appendSummaryPanel({ label: 'Session summary', content: '# Real summary' })!;
    const body = panel.querySelector('.summary-panel__body') as HTMLElement;
    expect(panel.getAttribute('role')).toBe('button');
    expect(panel.getAttribute('aria-expanded')).toBe('false');
    expect(body.hidden).toBe(true);
    panel.click();
    expect(panel.getAttribute('aria-expanded')).toBe('true');
    expect(body.hidden).toBe(false);
  });

  it('hides restored thinking that has no meaningful content', () => {
    appendThinkingBubble('---\n\n\u2060\n\n---');
    expect(document.querySelector('.message--thinking')).toBeNull();
  });
});
