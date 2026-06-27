// Compose box backed by a contenteditable <div> so attachments can live as
// inline, non-editable "pills" sitting between the user's words (at the caret),
// exactly where they were dropped/picked - instead of a separate chip tray or
// raw markdown text. The box still behaves like a plain text input for typing,
// slash-suggest and submit; serialization turns the mixed text+pill content
// into (a) a human-visible string for the chat bubble and (b) the model-facing
// string with each pill expanded to its attachment block.

export type AttachmentData = {
  name: string;
  type: string;
  size: number;
  path?: string;
  dataURI?: string;
  text?: string;
  isDir?: boolean;
};

const PILL_CLASS = 'att-pill';

// Base64-encode attachment metadata for the message body so restore-history can
// reconstruct the chip (kept identical to the previous textarea encoding).
function encodeAttachmentMeta(a: AttachmentData): string {
  const json = JSON.stringify({ name: a.name, type: a.type, size: a.size, path: a.path || '', isDir: a.isDir || false });
  return btoa(unescape(encodeURIComponent(json)));
}

function humanBytes(bytes: number): string {
  if (!bytes) return '';
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

// Short tag shown inside a pill, derived from type/extension.
function pillTag(a: AttachmentData): string {
  if (a.isDir) return 'DIR';
  if (a.type.startsWith('image/')) return 'IMG';
  const ext = (a.name.split('.').pop() || '').toUpperCase();
  if (ext && ext.length <= 4 && ext !== a.name.toUpperCase()) return ext;
  return 'FILE';
}

// The model-facing block for one attachment - mirrors the old buildAttachmentPrompt
// rules: image → base64 (+ path line when known); text → inline body (+ path);
// path-backed binary → a [Local file: …] pointer with NO base64 (avoids context
// flooding); pathless binary → base64 fallback.
function attachmentModelBlock(a: AttachmentData): string {
  const metadata = `<!--sapaloq-attachment:${encodeAttachmentMeta(a)}-->`;
  // Folders are path-only: the model gets a pointer it can list/read with its
  // own tools - never any contents.
  if (a.isDir) {
    return `${metadata}\n[Local folder: ${a.path || a.name}]`;
  }
  const pathLine = a.path ? `[Local file: ${a.path}]\n` : '';
  if (a.type.startsWith('image/') && a.dataURI) {
    return `${metadata}\n${pathLine}![${a.name}](${a.dataURI})`;
  }
  if (typeof a.text === 'string') {
    return `${metadata}\n${pathLine}--- file: ${a.name} (${a.type || 'text/plain'}) ---\n${a.text}\n--- end file: ${a.name} ---`;
  }
  if (a.path) {
    return `${metadata}\n[Local file: ${a.name} at ${a.path} (${a.type || 'application/octet-stream'}, ${humanBytes(a.size)})]`;
  }
  if (a.dataURI) {
    return `${metadata}\n![${a.name}](${a.dataURI})`;
  }
  return `${metadata}\n${pathLine}--- file: ${a.name} (${a.type || 'text/plain'}) ---\n${a.text || ''}\n--- end file: ${a.name} ---`;
}

// Reconstruct AttachmentData from a pill element's dataset.
function pillData(el: HTMLElement): AttachmentData {
  return {
    name: el.dataset.name || 'file',
    type: el.dataset.type || 'application/octet-stream',
    size: Number(el.dataset.size || 0),
    path: el.dataset.path || undefined,
    dataURI: el.dataset.datauri || undefined,
    text: el.dataset.text || undefined,
    isDir: el.dataset.isdir === '1' || undefined,
  };
}

// Build the inline, non-editable pill element.
function makePill(a: AttachmentData): HTMLElement {
  const pill = document.createElement('span');
  pill.className = PILL_CLASS;
  pill.contentEditable = 'false';
  pill.dataset.name = a.name;
  pill.dataset.type = a.type;
  pill.dataset.size = String(a.size);
  if (a.path) pill.dataset.path = a.path;
  if (a.dataURI) pill.dataset.datauri = a.dataURI;
  if (typeof a.text === 'string') pill.dataset.text = a.text;
  if (a.isDir) pill.dataset.isdir = '1';
  pill.title = a.path || a.name;

  const tag = document.createElement('span');
  tag.className = 'att-pill-tag';
  tag.textContent = pillTag(a);
  const label = document.createElement('span');
  label.className = 'att-pill-name';
  label.textContent = a.name;
  const close = document.createElement('button');
  close.type = 'button';
  close.className = 'att-pill-x';
  close.tabIndex = -1;
  close.setAttribute('aria-label', `hapus ${a.name}`);
  close.textContent = '×';

  pill.append(tag, label, close);
  return pill;
}

export type SerializedCompose = { visibleText: string; modelText: string; attachments: AttachmentData[] };

export class ComposeBox {
  readonly el: HTMLElement;
  private onChange: () => void;
  private onSubmit: () => void;
  // onKeyDown lets the caller intercept keys (e.g. slash-suggest navigation)
  // before the box's own Enter-submit / pill-delete logic runs. Returning true
  // means the caller consumed the event and the box must do nothing further.
  private onKeyDown: (e: KeyboardEvent) => boolean;

  constructor(
    el: HTMLElement,
    handlers: { onChange?: () => void; onSubmit?: () => void; onKeyDown?: (e: KeyboardEvent) => boolean } = {},
  ) {
    this.el = el;
    this.onChange = handlers.onChange || (() => {});
    this.onSubmit = handlers.onSubmit || (() => {});
    this.onKeyDown = handlers.onKeyDown || (() => false);
    this.wire();
  }

  private wire() {
    this.el.addEventListener('input', () => this.onChange());
    this.el.addEventListener('keyup', () => this.onChange());
    this.el.addEventListener('keydown', (event) => {
      const e = event as KeyboardEvent;
      // Give the caller (slash-suggest) first refusal on navigation keys so an
      // open popover swallows Enter/Tab/Arrows instead of submitting/blurring.
      if (this.onKeyDown(e)) return;
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        this.onSubmit();
        return;
      }
      // WebKitGTK quirk: pressing Backspace with the caret right after a pill
      // (a contenteditable=false node) first jerks the caret to the start of the
      // box instead of deleting the pill. Handle pill deletion explicitly so the
      // pill vanishes in one keystroke and the caret stays put.
      if (e.key === 'Backspace') {
        const pill = this.pillBeforeCaret();
        if (pill) {
          e.preventDefault();
          this.removePillKeepingCaret(pill);
          this.onChange();
        }
        return;
      }
      if (e.key === 'Delete') {
        const pill = this.pillAfterCaret();
        if (pill) {
          e.preventDefault();
          this.removePillKeepingCaret(pill);
          this.onChange();
        }
      }
    });
    // Force plain-text paste so foreign HTML never enters the box. File paste is
    // handled by the caller's clipboard logic (which calls insertAttachment).
    this.el.addEventListener('paste', (event) => {
      const e = event as ClipboardEvent;
      const items = Array.from(e.clipboardData?.items || []);
      if (items.some((it) => it.kind === 'file')) return; // let caller handle files
      e.preventDefault();
      const text = e.clipboardData?.getData('text/plain') || '';
      this.insertText(text);
      this.onChange();
    });
    // Click on a pill's × removes it.
    this.el.addEventListener('click', (event) => {
      const target = event.target as HTMLElement | null;
      if (target?.classList.contains('att-pill-x')) {
        event.preventDefault();
        target.closest(`.${PILL_CLASS}`)?.remove();
        this.el.focus();
        this.onChange();
      }
    });
  }

  focus() { this.el.focus(); }

  // Returns the pill immediately before a collapsed caret, if any. Handles both
  // "caret directly between nodes" and "caret at offset 0 of a text node that
  // follows a pill".
  private pillBeforeCaret(): HTMLElement | null {
    const sel = window.getSelection();
    if (!sel || sel.rangeCount === 0 || !sel.isCollapsed) return null;
    const range = sel.getRangeAt(0);
    if (!this.el.contains(range.startContainer)) return null;
    let node: Node | null = range.startContainer;
    let offset = range.startOffset;
    if (node.nodeType === Node.TEXT_NODE) {
      // Only delete the preceding pill when the caret sits at the very start of
      // (or in a whitespace-only gap at the start of) the text run.
      const before = (node.textContent || '').slice(0, offset);
      if (before.replace(/\u00a0/g, ' ').trim().length > 0) return null;
      const prev = node.previousSibling;
      return this.asPill(prev);
    }
    // Element/box container: the node just before `offset`.
    const prev = node.childNodes[offset - 1] || null;
    return this.asPill(prev);
  }

  private pillAfterCaret(): HTMLElement | null {
    const sel = window.getSelection();
    if (!sel || sel.rangeCount === 0 || !sel.isCollapsed) return null;
    const range = sel.getRangeAt(0);
    if (!this.el.contains(range.startContainer)) return null;
    const node: Node = range.startContainer;
    const offset = range.startOffset;
    if (node.nodeType === Node.TEXT_NODE) {
      if (offset < (node.textContent || '').length) return null;
      return this.asPill(node.nextSibling);
    }
    return this.asPill(node.childNodes[offset] || null);
  }

  private asPill(node: Node | null): HTMLElement | null {
    if (node && node.nodeType === Node.ELEMENT_NODE && (node as HTMLElement).classList.contains(PILL_CLASS)) {
      return node as HTMLElement;
    }
    return null;
  }

  // Remove a pill and leave the caret exactly where the pill was.
  private removePillKeepingCaret(pill: HTMLElement) {
    const sel = window.getSelection();
    const range = document.createRange();
    const parent = pill.parentNode;
    if (parent) {
      const idx = Array.prototype.indexOf.call(parent.childNodes, pill);
      pill.remove();
      range.setStart(parent, Math.max(0, idx));
      range.collapse(true);
      sel?.removeAllRanges();
      sel?.addRange(range);
    } else {
      pill.remove();
    }
    this.el.focus();
  }

  isEmpty(): boolean {
    return this.el.textContent?.trim() === '' && !this.el.querySelector(`.${PILL_CLASS}`);
  }

  clear() {
    this.el.innerHTML = '';
    this.onChange();
  }

  // Plain text content for slash-suggest (pills contribute nothing to slash).
  textValue(): string {
    let out = '';
    this.el.childNodes.forEach((node) => {
      if (node.nodeType === Node.TEXT_NODE) out += node.textContent || '';
      else if (node.nodeType === Node.ELEMENT_NODE && !(node as HTMLElement).classList.contains(PILL_CLASS)) {
        out += (node as HTMLElement).textContent || '';
      }
    });
    return out;
  }

  // Caret offset within the plain-text projection (best-effort; used by slash).
  caretOffset(): number {
    const sel = window.getSelection();
    if (!sel || sel.rangeCount === 0) return this.textValue().length;
    const range = sel.getRangeAt(0);
    if (!this.el.contains(range.startContainer)) return this.textValue().length;
    const pre = range.cloneRange();
    pre.selectNodeContents(this.el);
    pre.setEnd(range.startContainer, range.startOffset);
    return pre.toString().length;
  }

  // Restore caret to a plain-text offset (used after slash insertion).
  setCaretOffset(offset: number) {
    const sel = window.getSelection();
    if (!sel) return;
    let remaining = offset;
    const walker = document.createTreeWalker(this.el, NodeFilter.SHOW_TEXT);
    let node = walker.nextNode();
    while (node) {
      const len = node.textContent?.length || 0;
      if (remaining <= len) {
        const range = document.createRange();
        range.setStart(node, remaining);
        range.collapse(true);
        sel.removeAllRanges();
        sel.addRange(range);
        return;
      }
      remaining -= len;
      node = walker.nextNode();
    }
    // Fallback: caret at end.
    this.placeCaretAtEnd();
  }

  private placeCaretAtEnd() {
    const sel = window.getSelection();
    if (!sel) return;
    const range = document.createRange();
    range.selectNodeContents(this.el);
    range.collapse(false);
    sel.removeAllRanges();
    sel.addRange(range);
  }

  private currentRange(): Range {
    const sel = window.getSelection();
    if (sel && sel.rangeCount > 0 && this.el.contains(sel.getRangeAt(0).startContainer)) {
      return sel.getRangeAt(0);
    }
    // Default to end of box.
    const range = document.createRange();
    range.selectNodeContents(this.el);
    range.collapse(false);
    return range;
  }

  // Insert plain text at the caret.
  insertText(text: string) {
    if (!text) return;
    const range = this.currentRange();
    range.deleteContents();
    const node = document.createTextNode(text);
    range.insertNode(node);
    range.setStartAfter(node);
    range.collapse(true);
    const sel = window.getSelection();
    sel?.removeAllRanges();
    sel?.addRange(range);
  }

  // Replace the slash query (from slashIndex to caret) with a command prefix.
  replaceRange(fromOffset: number, toOffset: number, replacement: string) {
    // Simplest robust approach for our plain-ish content: rebuild leading text.
    const value = this.textValue();
    const next = value.slice(0, fromOffset) + replacement + value.slice(toOffset);
    // Preserve pills: only safe when the edited region is pure text. Slash always
    // operates on the trailing text run, so we re-set text nodes around pills.
    // For our usage the slash region never spans a pill, so a targeted text
    // replacement on the active text node is sufficient.
    this.setPlainTextPreservingPills(next, fromOffset + replacement.length);
  }

  // Rewrite the text-only projection while keeping pills in place. Used by slash
  // completion where the change is confined to text. Pills are re-appended in
  // their original order at the end if their positions can't be preserved; in
  // practice slash completion happens on a trailing text run with pills before
  // it, so order is preserved.
  private setPlainTextPreservingPills(_newText: string, caret: number) {
    // Capture pills in order.
    const pills = Array.from(this.el.querySelectorAll(`.${PILL_CLASS}`)) as HTMLElement[];
    // Rebuild: we only support the common case (text after the last pill).
    // Remove all text nodes, keep pills, then append new trailing text.
    // Compute text that belongs after the last pill vs before - but slash only
    // edits trailing text, so place full text after existing pills.
    this.el.innerHTML = '';
    pills.forEach((p) => this.el.appendChild(p));
    const textNode = document.createTextNode(_newText);
    this.el.appendChild(textNode);
    this.setCaretOffset(caret);
  }

  // Insert an attachment pill at the caret, with a trailing space so typing
  // continues naturally after it.
  insertAttachment(a: AttachmentData) {
    const range = this.currentRange();
    range.deleteContents();
    const pill = makePill(a);
    range.insertNode(pill);
    const space = document.createTextNode('\u00a0');
    range.setStartAfter(pill);
    range.insertNode(space);
    range.setStartAfter(space);
    range.collapse(true);
    const sel = window.getSelection();
    sel?.removeAllRanges();
    sel?.addRange(range);
    this.el.focus();
    this.onChange();
  }

  // Walk the box in DOM order and produce both projections.
  serialize(): SerializedCompose {
    const attachments: AttachmentData[] = [];
    let visible = '';
    let model = '';
    const walk = (nodes: NodeListOf<ChildNode> | ChildNode[]) => {
      nodes.forEach((node) => {
        if (node.nodeType === Node.TEXT_NODE) {
          const t = (node.textContent || '').replace(/\u00a0/g, ' ');
          visible += t;
          model += t;
        } else if (node.nodeType === Node.ELEMENT_NODE) {
          const el = node as HTMLElement;
          if (el.classList.contains(PILL_CLASS)) {
            const a = pillData(el);
            attachments.push(a);
            // Render path-backed attachments (native file/folder drops) as a
            // markdown link in the visible bubble so they are clickable - the
            // renderer routes the click through OpenExternal -> file manager.
            // Pathless attachments (browser/pasted) have no target, so keep the
            // bare name (the "N attachments" badge still surfaces them).
            visible += a.path ? `[${a.name}](${a.path})` : a.name;
            model += attachmentModelBlock(a);
          } else if (el.tagName === 'BR') {
            visible += '\n';
            model += '\n';
          } else {
            walk(el.childNodes);
          }
        }
      });
    };
    walk(this.el.childNodes);
    return { visibleText: visible.trim(), modelText: model.trim(), attachments };
  }
}

// Exposed for unit testing the serialization rules without a real ComposeBox.
export const __test = { attachmentModelBlock, pillTag };
