// Slash-command suggestion popover. Detects an active `/query` at the caret and
// renders matching command suggestions returned by the core.
import { SlashSuggest } from '../../wailsjs/go/main/App';
import type { CommandEntry } from '../core/types';
import { getCompose } from '../core/state';
import { autosizeCompose } from '../ui/compose-ui';

const SLASH_BOUNDARY = /(^\/|\s\/|\n\/)/;

export function activeSlashAtChat(value: string, caret: number): { query: string; slashIndex: number } | null {
  const before = value.slice(0, caret);
  const slashIndex = before.lastIndexOf('/');
  if (slashIndex < 0) return null;
  const boundary = before.slice(Math.max(0, slashIndex - 1), slashIndex + 1);
  if (slashIndex > 0 && !SLASH_BOUNDARY.test(boundary)) return null;
  const afterSlash = before.slice(slashIndex + 1);
  if (/\s/.test(afterSlash)) return null;
  return { query: afterSlash, slashIndex };
}

export function hideSlashSuggest() {
  const popover = document.getElementById('slash-popover');
  if (popover) popover.innerHTML = '';
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
    const suggestions = (await SlashSuggest(active.query)) as CommandEntry[];
    popover.innerHTML = suggestions
      .filter((entry) => entry.enabled !== false)
      .map((entry) => `<button type="button" class="slash-item" data-prefix="${entry.prefix}"><strong>${entry.label}</strong><span>${entry.description}</span></button>`)
      .join('');
    popover.querySelectorAll<HTMLButtonElement>('.slash-item').forEach((button) => {
      button.addEventListener('click', () => {
        const prefix = button.dataset.prefix || '';
        compose.replaceRange(active.slashIndex, caret, prefix);
        compose.focus();
        autosizeCompose();
        hideSlashSuggest();
      });
    });
  } catch {
    hideSlashSuggest();
  }
}
