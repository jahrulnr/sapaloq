import { describe, expect, it } from 'vitest';
import { renderTranscriptEntry } from './render';

describe('renderTranscriptEntry thinking', () => {
  it('shows thinking body when details is open (not message--thinking gated)', () => {
    const el = renderTranscriptEntry({
      kind: 'thinking',
      id: 'turn-2',
      text: 'The user wants help with the plan file.',
    });
    expect(el.classList.contains('message--thinking')).toBe(false);
    el.setAttribute('open', '');
    const body = el.querySelector('.transcript-entry-body');
    expect(body?.textContent).toContain('plan file');
  });
});
