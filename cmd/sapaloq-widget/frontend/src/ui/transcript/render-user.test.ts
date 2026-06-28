import { describe, it, expect, vi } from 'vitest';

vi.mock('../../wailsjs/go/main/App', () => ({
  OpenExternal: vi.fn(),
  OpenAttachment: vi.fn(),
  SubmitFeedback: vi.fn(),
}));

import { renderTranscriptEntry } from './render';

describe('renderTranscriptEntry user bubble', () => {
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

  it('renders a pasted image attachment under the user bubble', () => {
    const dataURI = 'data:image/png;base64,iVBORw0KGgo=';
    const el = renderTranscriptEntry({
      kind: 'user',
      text: `cek ui\n![screenshot.png](${dataURI})`,
    });
    expect(el.querySelector('.message-attachments')).not.toBeNull();
    expect(el.querySelector('.message-attachment-row img')?.getAttribute('src')).toBe(dataURI);
    expect(el.textContent).toContain('cek ui');
    expect(el.textContent).not.toContain('data:image');
  });
});
