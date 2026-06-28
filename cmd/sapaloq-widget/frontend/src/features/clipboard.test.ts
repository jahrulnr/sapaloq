import { describe, it, expect, vi } from 'vitest';
import { collectClipboardImageFiles, transferHasAttachable } from './clipboard';

describe('transferHasAttachable', () => {
  it('detects file items', () => {
    const file = new File(['x'], 'a.png', { type: 'image/png' });
    const transfer = {
      files: [file],
      items: [{ kind: 'file', type: 'image/png', getAsFile: () => file }],
    } as unknown as DataTransfer;
    expect(transferHasAttachable(transfer)).toBe(true);
  });

  it('detects WebKitGTK image string items', () => {
    const transfer = {
      files: [],
      items: [{ kind: 'string', type: 'image/png' }],
    } as unknown as DataTransfer;
    expect(transferHasAttachable(transfer)).toBe(true);
  });

  it('returns false for plain text only', () => {
    const transfer = {
      files: [],
      items: [{ kind: 'string', type: 'text/plain' }],
    } as unknown as DataTransfer;
    expect(transferHasAttachable(transfer)).toBe(false);
  });
});

describe('collectClipboardImageFiles', () => {
  it('builds File objects from image string clipboard items', async () => {
    const blob = new Blob(['png-bytes'], { type: 'image/png' });
    const transfer = {
      items: [{
        kind: 'string',
        type: 'image/png',
        getType: vi.fn(async () => blob),
      }],
    } as unknown as DataTransfer;
    const files = await collectClipboardImageFiles(transfer);
    expect(files).toHaveLength(1);
    expect(files[0].type).toBe('image/png');
    expect(files[0].name).toBe('pasted-image.png');
  });
});
