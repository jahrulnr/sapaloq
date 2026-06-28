import { describe, expect, it } from 'vitest';
import { applyDeltaOps } from './apply-transcript-delta';
import type { TranscriptPaneState } from './types';

function mountBody(): HTMLElement {
  const body = document.createElement('div');
  document.body.append(body);
  return body;
}

describe('applyDeltaOps', () => {
  it('upserts then append_text by entry id', () => {
    const body = mountBody();
    const state: TranscriptPaneState = { renderedEntryCount: 0 };
    applyDeltaOps(body, state, [
      { op: 'upsert', entry: { id: '7-pending-text', kind: 'text', text: '' } },
      { op: 'append_text', entry_id: '7-pending-text', delta: 'Hello' },
      { op: 'append_text', entry_id: '7-pending-text', delta: ' world' },
    ]);
    const row = body.querySelector('[data-entry-id="7-pending-text"]') as HTMLElement | null;
    expect(row).toBeTruthy();
    expect(row?.dataset.rawText).toBe('Hello world');
    expect(state.renderedEntryCount).toBe(1);
  });

  it('append_text without prior upsert creates a fallback row', () => {
    const body = mountBody();
    const state: TranscriptPaneState = { renderedEntryCount: 0 };
    applyDeltaOps(body, state, [{ op: 'append_text', entry_id: '9-pending-thinking', delta: 'hmm' }]);
    const row = body.querySelector('[data-entry-id="9-pending-thinking"]');
    expect(row?.getAttribute('data-entry-kind')).toBe('thinking');
  });

  it('remove drops the row', () => {
    const body = mountBody();
    const state: TranscriptPaneState = { renderedEntryCount: 0 };
    applyDeltaOps(body, state, [
      { op: 'upsert', entry: { id: 'rm-1', kind: 'text', text: 'x' } },
      { op: 'remove', entry_id: 'rm-1' },
    ]);
    expect(body.querySelector('[data-entry-id="rm-1"]')).toBeNull();
    expect(state.renderedEntryCount).toBe(0);
  });
});
