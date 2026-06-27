import { describe, it, expect, vi, beforeEach } from 'vitest';

const clipboardGetText = vi.fn(async () => '');
const clipboardGetImage = vi.fn<[], Promise<{ data_uri: string; mime: string; size: number } | null>>(async () => null);
vi.mock('../../wailsjs/runtime/runtime', () => ({
  ClipboardGetText: () => clipboardGetText(),
}));
vi.mock('../../wailsjs/go/main/App', () => ({
  ClipboardGetImage: () => clipboardGetImage(),
}));
vi.mock('./attachments', () => ({
  addFiles: vi.fn(async () => {}),
  collectTransferFiles: vi.fn(() => []),
  dataURIToFile: vi.fn(() => null),
}));
vi.mock('./clipboard', () => ({
  collectClipboardImageFiles: vi.fn(async () => []),
}));

import { ingestComposePaste } from './compose-paste';

function makeCompose() {
  const inserted: string[] = [];
  const attachments: unknown[] = [];
  return {
    insertText: (text: string) => { inserted.push(text); },
    insertAttachment: (att: unknown) => { attachments.push(att); },
    inserted,
    attachments,
  };
}

describe('ingestComposePaste', () => {
  beforeEach(() => {
    clipboardGetText.mockReset();
    clipboardGetImage.mockReset();
    clipboardGetText.mockResolvedValue('');
    clipboardGetImage.mockResolvedValue(null);
  });

  it('inserts plain text from the paste event DataTransfer', async () => {
    const compose = makeCompose();
    const transfer = {
      files: [],
      items: [],
      getData: (type: string) => (type === 'text/plain' ? 'https://example.com' : ''),
    } as unknown as DataTransfer;
    await expect(ingestComposePaste(compose as never, transfer)).resolves.toBe(true);
    expect(compose.inserted).toEqual(['https://example.com']);
  });

  it('falls back to Wails ClipboardGetText for context-menu paste', async () => {
    const compose = makeCompose();
    clipboardGetText.mockResolvedValue('https://sapaloq.dev/docs');
    await expect(ingestComposePaste(compose as never)).resolves.toBe(true);
    expect(compose.inserted).toEqual(['https://sapaloq.dev/docs']);
  });

  it('inserts a GTK clipboard image via ClipboardGetImage', async () => {
    const compose = makeCompose();
    clipboardGetImage.mockResolvedValue({
      data_uri: 'data:image/png;base64,abc',
      mime: 'image/png',
      size: 3,
    });
    await expect(ingestComposePaste(compose as never)).resolves.toBe(true);
    expect(compose.attachments).toHaveLength(1);
  });
});
