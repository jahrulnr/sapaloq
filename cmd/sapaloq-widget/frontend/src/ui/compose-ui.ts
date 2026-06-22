// Compose-box presentation helpers: enable/disable while streaming, autosize to
// content, and the expand/collapse toggle. Kept separate from the chat
// controller so slash-suggest can reuse autosize without a circular import.
import { ICON_COLLAPSE, ICON_EXPAND } from './icons';
import { getComposeInput } from './dom';

// Lock/unlock the contenteditable compose box while a response streams.
export function setComposeDisabled(disabled: boolean) {
  const input = getComposeInput();
  if (!input) return;
  input.setAttribute('contenteditable', disabled ? 'false' : 'true');
  input.classList.toggle('is-disabled', disabled);
}

// Grow the textarea to fit its content up to the CSS max-height (--compose-max,
// or --compose-max-tall when the composer is in the expanded state), à la ChatGPT.
// Toggles `.is-tall` on the footer once the content actually overflows the normal
// cap, which reveals the expand button.
// The contenteditable box grows naturally up to its CSS max-height (then
// scrolls). We only need to toggle `.is-tall` once the content overflows the
// normal cap so the expand button appears.
export function autosizeCompose() {
  const input = getComposeInput();
  if (!input) return;
  const footer = input.closest('.popup-compose');
  const wrap = input.closest('.compose-wrap');
  const overflowing = input.scrollHeight > input.clientHeight + 1;
  const isExpandedState = wrap?.classList.contains('expanded') ?? false;
  footer?.classList.toggle('is-tall', overflowing || isExpandedState);
}

export function resetComposeSize() {
  const wrap = getComposeInput()?.closest('.compose-wrap');
  const footer = getComposeInput()?.closest('.popup-compose');
  const expandBtn = document.getElementById('compose-expand');
  wrap?.classList.remove('expanded');
  footer?.classList.remove('is-tall');
  expandBtn?.setAttribute('aria-pressed', 'false');
  if (expandBtn) expandBtn.innerHTML = ICON_EXPAND;
}

export function toggleComposeExpand() {
  const input = getComposeInput();
  const wrap = input?.closest('.compose-wrap');
  const expandBtn = document.getElementById('compose-expand');
  if (!wrap || !expandBtn) return;
  const next = !wrap.classList.contains('expanded');
  wrap.classList.toggle('expanded', next);
  expandBtn.setAttribute('aria-pressed', String(next));
  expandBtn.setAttribute('aria-label', next ? 'Perkecil input' : 'Perbesar input');
  expandBtn.title = next ? 'Perkecil input' : 'Perbesar input';
  expandBtn.innerHTML = next ? ICON_COLLAPSE : ICON_EXPAND;
  autosizeCompose();
  input?.focus();
}
