import { describe, it, expect, vi } from 'vitest';

// ComposeBox/markdown reach window.go.* only inside click handlers, never during
// the pure serialization/render paths exercised here - mock the bindings so the
// imports resolve without a live Wails runtime.
vi.mock('../../wailsjs/go/main/App', () => ({
  OpenExternal: vi.fn(),
  OpenAttachment: vi.fn(),
  SubmitFeedback: vi.fn(),
}));

import { ComposeBox, __test, type AttachmentData } from './compose';
import { parseTurnContent } from '../features/messages';

function makeCompose(): ComposeBox {
  const el = document.createElement('div');
  document.body.append(el);
  return new ComposeBox(el);
}

describe('compose pill tag', () => {
  it('tags a folder attachment DIR', () => {
    const dir: AttachmentData = { name: 'sapaloq', type: 'inode/directory', size: 0, path: '/home/x/sapaloq', isDir: true };
    expect(__test.pillTag(dir)).toBe('DIR');
  });

  it('tags an image IMG and a plain file by extension', () => {
    expect(__test.pillTag({ name: 'a.png', type: 'image/png', size: 1 })).toBe('IMG');
    expect(__test.pillTag({ name: 'notes.md', type: 'text/markdown', size: 1 })).toBe('MD');
  });
});

describe('compose attachment model block', () => {
  it('emits a path-only [Local folder: …] pointer for a folder (never contents)', () => {
    const dir: AttachmentData = { name: 'sapaloq', type: 'inode/directory', size: 0, path: '/home/x/sapaloq', isDir: true };
    const block = __test.attachmentModelBlock(dir);
    expect(block).toContain('[Local folder: /home/x/sapaloq]');
    expect(block).not.toContain('--- file:');
  });
});

describe('compose serialize() - attachments render as markdown links in the bubble', () => {
  it('emits [name](path) in visibleText for a path-backed file (the bubble-link bug fix)', () => {
    const compose = makeCompose();
    compose.insertAttachment({ name: 'main.go', type: 'text/x-go', size: 12, path: '/home/x/main.go', text: 'package main' });
    const out = compose.serialize();
    expect(out.visibleText).toContain('[main.go](/home/x/main.go)');
    // model side still carries the inline file block for the LLM.
    expect(out.modelText).toContain('--- file: main.go');
  });

  it('emits [name](path) in visibleText for a dropped folder', () => {
    const compose = makeCompose();
    compose.insertAttachment({ name: 'sapaloq', type: 'inode/directory', size: 0, path: '/home/x/sapaloq', isDir: true });
    const out = compose.serialize();
    expect(out.visibleText).toContain('[sapaloq](/home/x/sapaloq)');
    expect(out.modelText).toContain('[Local folder: /home/x/sapaloq]');
  });

  it('keeps the bare name (no link) for a pathless/pasted attachment', () => {
    const compose = makeCompose();
    compose.insertAttachment({ name: 'pasted.txt', type: 'text/plain', size: 3, text: 'hi' });
    const out = compose.serialize();
    expect(out.visibleText).toContain('pasted.txt');
    expect(out.visibleText).not.toContain('](');
  });
});

describe('parseTurnContent - restored bubbles keep the link', () => {
  it('reconstructs [name](path) from a persisted path-backed file turn', () => {
    const compose = makeCompose();
    compose.insertAttachment({ name: 'main.go', type: 'text/x-go', size: 12, path: '/home/x/main.go', text: 'package main' });
    const model = compose.serialize().modelText; // what gets persisted as the user turn
    const parsed = parseTurnContent(model);
    expect(parsed.text).toContain('[main.go](/home/x/main.go)');
    // the model-facing inline body + [Local file:] pointer are stripped from display
    expect(parsed.text).not.toContain('--- file:');
    expect(parsed.text).not.toContain('[Local file:');
    expect(parsed.attachments.some((a) => a.name === 'main.go' && a.path === '/home/x/main.go')).toBe(true);
  });

  it('reconstructs [name](path) and strips [Local folder:] for a persisted folder turn', () => {
    const compose = makeCompose();
    compose.insertAttachment({ name: 'sapaloq', type: 'inode/directory', size: 0, path: '/home/x/sapaloq', isDir: true });
    const model = compose.serialize().modelText;
    const parsed = parseTurnContent(model);
    expect(parsed.text).toContain('[sapaloq](/home/x/sapaloq)');
    expect(parsed.text).not.toContain('[Local folder:');
    expect(parsed.attachments.some((a) => a.name === 'sapaloq' && a.isDir)).toBe(true);
  });
});
