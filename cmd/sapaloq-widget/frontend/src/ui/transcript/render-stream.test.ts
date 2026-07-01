import { describe, expect, it } from 'vitest';
import { appendTextDelta, flushTextDeltaMarkdown, patchTranscriptEntry } from './render';

function mountTextEntry(): HTMLElement {
  const el = document.createElement('div');
  el.className = 'transcript-entry transcript-text';
  el.dataset.entryKind = 'text';
  el.dataset.entryId = 'gen-pending-text';
  const body = document.createElement('div');
  body.className = 'transcript-entry-body';
  el.append(body);
  document.body.append(el);
  return el;
}

describe('appendTextDelta streaming', () => {
  it('keeps cumulative text after a markdown flush and the next delta', () => {
    const el = mountTextEntry();
    appendTextDelta(el, 'chunk1');
    flushTextDeltaMarkdown(el);
    expect(el.querySelector('.stream-plain')).toBeNull();

    appendTextDelta(el, 'chunk2');
    expect(el.dataset.rawText).toBe('chunk1chunk2');
    expect(el.querySelector('.transcript-entry-body')?.textContent).toBe('chunk1chunk2');
  });

  it('appends across multiple fast deltas without losing earlier chunks', () => {
    const el = mountTextEntry();
    appendTextDelta(el, 'a');
    appendTextDelta(el, 'b');
    appendTextDelta(el, 'c');
    expect(el.dataset.rawText).toBe('abc');
    expect(el.querySelector('.stream-plain')?.textContent).toBe('abc');
  });
});

describe('patchTranscriptEntry during streaming', () => {
  it('ignores stale snapshot text shorter than the live buffer', () => {
    const el = mountTextEntry();
    appendTextDelta(el, 'live longer text');
    patchTranscriptEntry(el, { kind: 'text', text: 'live' });
    expect(el.dataset.rawText).toBe('live longer text');
    expect(el.querySelector('.stream-plain')?.textContent).toBe('live longer text');
  });
});
