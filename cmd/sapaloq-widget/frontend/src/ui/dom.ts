// Small, dependency-free DOM/format helpers shared across modules.

export function getComposeInput() {
  return document.getElementById('compose-input') as HTMLElement | null;
}

export function getMessageList() {
  return document.getElementById('message-list');
}

export type MessageScrollSnapshot = {
  atBottom: boolean;
  scrollTop: number;
};

// A tiny tolerance absorbs fractional layout rounding without treating a
// reader who intentionally moved up the transcript as still following it.
const MESSAGE_BOTTOM_TOLERANCE_PX = 2;

/** Capture before changing transcript DOM; checking after append is too late. */
export function captureMessageScroll(list = getMessageList()): MessageScrollSnapshot {
  if (!list) return { atBottom: true, scrollTop: 0 };
  return {
    atBottom: list.scrollHeight - list.scrollTop - list.clientHeight <= MESSAGE_BOTTOM_TOLERANCE_PX,
    scrollTop: list.scrollTop,
  };
}

/** Follow new content only when the reader was already at the transcript end. */
export function restoreMessageScroll(snapshot: MessageScrollSnapshot, list = getMessageList()) {
  if (!list) return;
  list.scrollTop = snapshot.atBottom ? list.scrollHeight : snapshot.scrollTop;
}

// hasVisibleText reports whether an element renders any non-whitespace text.
// Used to drop assistant bubbles that ended up empty (e.g. a stray empty
// response delta) so we never show a blank bubble or attach feedback to it.
export function hasVisibleText(el: HTMLElement | null | undefined): boolean {
  if (!el) return false;
  const text = (el.textContent || '').replace(/[\s\u200B-\u200D\u2060\uFEFF]+/g, '');
  if (text.length > 0) return true;
  // Media is meaningful even without textContent. Structural markdown such as
  // <hr>/<br> is deliberately excluded: a model response made only of `---`
  // separators should not occupy the transcript as an apparently blank block.
  return !!el.querySelector('img, video, audio, canvas, svg');
}

// Normalize text just before markdown rendering. We deliberately do NOT strip
// emoji/pictographs here: models routinely use ✅/❌/✓/✗ (often with a U+FE0F
// variation selector) to fill table cells and checklists, and stripping them
// left those cells visually empty. marked + DOMPurify render these glyphs
// safely, so we only trim trailing whitespace.
export function sanitizeDisplayText(text: string) {
  return text.replace(/\s+$/g, '');
}

export function formatTokens(value: number) {
  if (value >= 1000000) {
    const millions = value / 1000000;
    return `${Number.isInteger(millions) ? millions : millions.toFixed(1)}M`;
  }
  if (value >= 1000) return `${Math.round(value / 1000)}k`;
  return `${value}`;
}

export function formatBytes(bytes: number) {
  if (!bytes) return '';
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

export function errorText(err: unknown) {
  if (err instanceof Error && err.message) return err.message;
  if (typeof err === 'string') return err;
  return 'unknown error';
}
