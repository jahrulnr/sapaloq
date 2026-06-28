// WebKitGTK/Wails frameless windows often suppress the native context menu on
// contenteditable fields. Provide a minimal Linux-style menu (Cut/Copy/Paste).

import { getCompose } from '../core/state';
import { ingestComposePaste } from '../features/compose-paste';

let menuEl: HTMLElement | null = null;

function hideMenu() {
  menuEl?.remove();
  menuEl = null;
}

function hasComposeSelection(el: HTMLElement): boolean {
  const sel = window.getSelection();
  if (!sel || sel.isCollapsed || !sel.rangeCount) return false;
  const range = sel.getRangeAt(0);
  return el.contains(range.commonAncestorContainer);
}

async function pasteIntoCompose(el: HTMLElement) {
  el.focus();
  const compose = getCompose();
  if (!compose) return;
  const handled = await ingestComposePaste(compose);
  if (handled) el.dispatchEvent(new Event('input', { bubbles: true }));
}

function showMenu(el: HTMLElement, x: number, y: number) {
  hideMenu();
  const canEdit = el.getAttribute('contenteditable') !== 'false';
  const hasSelection = hasComposeSelection(el);

  menuEl = document.createElement('div');
  menuEl.className = 'compose-context-menu';
  menuEl.setAttribute('role', 'menu');

  const items: { label: string; enabled: boolean; action: () => void }[] = [
    {
      label: 'Undo',
      enabled: canEdit && document.queryCommandEnabled('undo'),
      action: () => { el.focus(); document.execCommand('undo'); },
    },
    {
      label: 'Redo',
      enabled: canEdit && document.queryCommandEnabled('redo'),
      action: () => { el.focus(); document.execCommand('redo'); },
    },
    { label: 'Cut', enabled: canEdit && hasSelection, action: () => { el.focus(); document.execCommand('cut'); } },
    { label: 'Copy', enabled: hasSelection, action: () => { el.focus(); document.execCommand('copy'); } },
    { label: 'Paste', enabled: canEdit, action: () => { void pasteIntoCompose(el); } },
    {
      label: 'Select all',
      enabled: true,
      action: () => {
        el.focus();
        const range = document.createRange();
        range.selectNodeContents(el);
        const sel = window.getSelection();
        sel?.removeAllRanges();
        sel?.addRange(range);
      },
    },
  ];

  for (const item of items) {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'compose-context-menu-item';
    button.textContent = item.label;
    button.disabled = !item.enabled;
    button.setAttribute('role', 'menuitem');
    button.addEventListener('click', (event) => {
      event.stopPropagation();
      if (!item.enabled) return;
      hideMenu();
      item.action();
    });
    menuEl.append(button);
  }

  document.body.append(menuEl);
  const pad = 8;
  const rect = menuEl.getBoundingClientRect();
  const left = Math.min(x, window.innerWidth - rect.width - pad);
  const top = Math.min(y, window.innerHeight - rect.height - pad);
  menuEl.style.left = `${Math.max(pad, left)}px`;
  menuEl.style.top = `${Math.max(pad, top)}px`;

  const dismiss = () => hideMenu();
  setTimeout(() => {
    document.addEventListener('click', dismiss, { once: true });
    document.addEventListener('contextmenu', dismiss, { once: true });
    window.addEventListener('blur', dismiss, { once: true });
  }, 0);
}

export function initComposeContextMenu(el: HTMLElement) {
  el.addEventListener('contextmenu', (event) => {
    if (el.getAttribute('contenteditable') === 'false') return;
    event.preventDefault();
    event.stopPropagation();
    showMenu(el, event.clientX, event.clientY);
  });
}

export function __testHideComposeContextMenu() {
  hideMenu();
}
