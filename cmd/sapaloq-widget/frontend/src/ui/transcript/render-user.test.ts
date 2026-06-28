import { describe, it, expect, vi } from 'vitest';

vi.mock('../../wailsjs/go/main/App', () => ({
  OpenExternal: vi.fn(),
  OpenAttachment: vi.fn(),
  SubmitFeedback: vi.fn(),
}));

import { renderTranscriptEntry } from './render';

describe('renderTranscriptEntry user folder links', () => {
  it('renders a dropped folder as a clickable markdown link in the bubble', () => {
    const el = renderTranscriptEntry({
      kind: 'user',
      text: 'lihat [Local folder: /apps/template/profile] dulu',
    });
    const a = el.querySelector('a');
    expect(a).not.toBeNull();
    expect(a?.getAttribute('href')).toBe('/apps/template/profile');
    expect(a?.textContent).toBe('profile');
    expect(el.textContent).not.toContain('[Local folder:');
  });
});
