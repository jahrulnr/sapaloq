// Slash-command suggestion popover. Detects an active `/query` at the caret and
// renders matching command suggestions returned by the core. Supports keyboard
// navigation: ArrowUp/Down moves the highlighted item, Tab/Enter accepts it,
// Escape closes the popover. Mouse click still accepts an item directly.
import { SlashSuggest } from '../../wailsjs/go/main/App';
import type { CommandEntry } from '../core/types';
import { getCompose } from '../core/state';
import { autosizeCompose } from '../ui/compose-ui';

const SLASH_BOUNDARY = /(^\/|\s\/|\n\/)/;

// Popover state for keyboard navigation. `entries` mirrors what is rendered
// (already filtered to enabled items); `activeIndex` is the highlighted row;
// `range` is the text span the accepted prefix replaces.
let entries: CommandEntry[] = [];
let activeIndex = 0;
let activeRange: { slashIndex: number; caret: number } | null = null;

export function activeSlashAtChat(value: string, caret: number): { query: string; slashIndex: number } | null {
  const before = value.slice(0, caret);
  const slashIndex = before.lastIndexOf('/');
  if (slashIndex < 0) return null;
  const boundary = before.slice(Math.max(0, slashIndex - 1), slashIndex + 1);
  if (slashIndex > 0 && !SLASH_BOUNDARY.test(boundary)) return null;
  const afterSlash = before.slice(slashIndex + 1);
  if (/\s/.test(afterSlash)) {
    // A space ends the command name itself, but "/model <key>" style argument
    // suggestions still want the popover open. Keep it open as long as there is
    // no newline (a newline always closes it).
    if (/\n/.test(afterSlash)) return null;
  }
  return { query: afterSlash, slashIndex };
}

export function isSlashOpen(): boolean {
  return entries.length > 0;
}

export function hideSlashSuggest() {
  entries = [];
  activeIndex = 0;
  activeRange = null;
  const popover = document.getElementById('slash-popover');
  if (popover) popover.innerHTML = '';
}

// Repaint the `is-active` highlight without re-querying the core.
function paintActive() {
  const popover = document.getElementById('slash-popover');
  if (!popover) return;
  popover.querySelectorAll<HTMLButtonElement>('.slash-item').forEach((button, index) => {
    button.classList.toggle('is-active', index === activeIndex);
    // scrollIntoView is absent in some test environments (jsdom); guard it.
    if (index === activeIndex && typeof button.scrollIntoView === 'function') {
      button.scrollIntoView({ block: 'nearest' });
    }
  });
}

// Insert the active suggestion's prefix into the compose box.
function acceptActive() {
  const compose = getCompose();
  if (!compose || !activeRange || !entries[activeIndex]) return;
  const prefix = entries[activeIndex].prefix || '';
  compose.replaceRange(activeRange.slashIndex, activeRange.caret, prefix);
  compose.focus();
  autosizeCompose();
  hideSlashSuggest();
  // Re-query so argument suggestions (e.g. provider list after "/model")
  // appear immediately once the command name is completed.
  void refreshSlashSuggest();
}

// Keyboard handler delegated from the compose box. Returns true when it
// consumed the event (so the caller must not run its own Enter/Tab logic).
export function slashKeydown(e: KeyboardEvent): boolean {
  if (!isSlashOpen()) return false;
  switch (e.key) {
    case 'ArrowDown':
      e.preventDefault();
      activeIndex = (activeIndex + 1) % entries.length;
      paintActive();
      return true;
    case 'ArrowUp':
      e.preventDefault();
      activeIndex = (activeIndex - 1 + entries.length) % entries.length;
      paintActive();
      return true;
    case 'Tab':
    case 'Enter':
      e.preventDefault();
      acceptActive();
      return true;
    case 'Escape':
      e.preventDefault();
      hideSlashSuggest();
      return true;
    default:
      return false;
  }
}

export async function refreshSlashSuggest() {
  const compose = getCompose();
  const popover = document.getElementById('slash-popover');
  if (!compose || !popover) return;
  const caret = compose.caretOffset();
  const active = activeSlashAtChat(compose.textValue(), caret);
  if (!active) {
    hideSlashSuggest();
    return;
  }
  try {
    const suggestions = ((await SlashSuggest(active.query)) as CommandEntry[]).filter(
      (entry) => entry.enabled !== false,
    );
    if (suggestions.length === 0) {
      hideSlashSuggest();
      return;
    }
    entries = suggestions;
    activeIndex = 0;
    activeRange = { slashIndex: active.slashIndex, caret };
		popover.replaceChildren();
		for (const entry of suggestions) {
			const button = document.createElement('button');
			button.type = 'button';
			button.className = 'slash-item';
			button.dataset.prefix = entry.prefix;
			const label = document.createElement('strong');
			label.textContent = entry.label;
			const description = document.createElement('span');
			description.textContent = entry.description;
			button.append(label, description);
			popover.append(button);
		}
    popover.querySelectorAll<HTMLButtonElement>('.slash-item').forEach((button, index) => {
      button.addEventListener('mouseenter', () => {
        activeIndex = index;
        paintActive();
      });
      button.addEventListener('click', () => {
        activeIndex = index;
        acceptActive();
      });
    });
    paintActive();
  } catch {
    hideSlashSuggest();
  }
}
