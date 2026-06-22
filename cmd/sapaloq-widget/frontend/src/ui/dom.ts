// Small, dependency-free DOM/format helpers shared across modules.

export function getComposeInput() {
  return document.getElementById('compose-input') as HTMLElement | null;
}

export function getMessageList() {
  return document.getElementById('message-list');
}

export function scrollMessagesToBottom() {
  const list = getMessageList();
  if (list) list.scrollTop = list.scrollHeight;
}

// hasVisibleText reports whether an element renders any non-whitespace text.
// Used to drop assistant bubbles that ended up empty (e.g. a stray empty
// response delta) so we never show a blank bubble or attach feedback to it.
export function hasVisibleText(el: HTMLElement | null | undefined): boolean {
  if (!el) return false;
  return (el.textContent || '').replace(/\s+/g, '').length > 0;
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
