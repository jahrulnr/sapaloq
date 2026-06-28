import type { ToolActivityMode } from './tool-activity';
import { appendTextDelta, flushTextDeltaMarkdown, patchTranscriptEntry, renderTranscriptEntry } from './render';
import type { TranscriptEntry, TranscriptPatchOp, TranscriptPaneState } from './types';
import { createTranscriptPane } from './sync-pane';

function entryKindFromID(entryID: string): TranscriptEntry['kind'] {
  return entryID.includes('thinking') ? 'thinking' : 'text';
}

function findEntryElement(pane: HTMLElement, entryID: string): HTMLElement | null {
  const nodes = pane.querySelectorAll('[data-entry-id]');
  for (let i = 0; i < nodes.length; i++) {
    const node = nodes[i];
    if (node instanceof HTMLElement && node.dataset.entryId === entryID) return node;
  }
  return null;
}

export function applyDeltaOps(
  body: HTMLElement,
  state: TranscriptPaneState,
  ops: ReadonlyArray<TranscriptPatchOp>,
  mode: ToolActivityMode = 'chat',
) {
  body.querySelectorAll('.transcript-empty').forEach((node) => node.remove());
  let pane = body.querySelector('.transcript-pane') as HTMLElement | null;
  if (!pane) {
    pane = createTranscriptPane();
    body.append(pane);
    state.renderedEntryCount = 0;
  }

  for (const op of ops) {
    switch (op.op) {
      case 'upsert': {
        const entry = op.entry;
        if (!entry?.id) break;
        const existing = findEntryElement(pane, entry.id);
        if (!existing) {
          const el = renderTranscriptEntry(entry, mode);
          if (!el.classList.contains('is-empty')) pane.append(el);
        } else {
          patchTranscriptEntry(existing, entry, mode);
        }
        break;
      }
      case 'append_text': {
        if (!op.entry_id || !op.delta) break;
        let el = findEntryElement(pane, op.entry_id);
        if (!el) {
          el = renderTranscriptEntry({ id: op.entry_id, kind: entryKindFromID(op.entry_id), text: '' }, mode);
          pane.append(el);
        }
        appendTextDelta(el, op.delta);
        break;
      }
      case 'remove': {
        if (!op.entry_id) break;
        findEntryElement(pane, op.entry_id)?.remove();
        break;
      }
    }
  }
  state.renderedEntryCount = pane.children.length;
}

export function flushDeltaMarkdownInPane(body: HTMLElement) {
  body.querySelectorAll('.transcript-pane [data-entry-id]').forEach((node) => {
    if (node instanceof HTMLElement) flushTextDeltaMarkdown(node);
  });
}
