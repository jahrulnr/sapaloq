import { describe, it, expect, vi, beforeEach } from 'vitest';
import type { CommandEntry } from '../core/types';

// Suggestions the mocked core returns for "/model".
const MODEL_SUGGESTIONS: CommandEntry[] = [
  { id: 'model', prefix: '/model minimax-free', pattern: '', label: 'Model: minimax-free', description: 'blackboxai · minimax', category: 'models', enabled: true } as CommandEntry,
  { id: 'model', prefix: '/model kimi', pattern: '', label: 'Model: kimi', description: 'moonshot · kimi-k2', category: 'models', enabled: true } as CommandEntry,
];

const slashSuggest = vi.fn(async (_query: string) => MODEL_SUGGESTIONS as CommandEntry[]);
vi.mock('../../wailsjs/go/main/App', () => ({ SlashSuggest: (q: string) => slashSuggest(q) }));
vi.mock('../ui/compose-ui', () => ({ autosizeCompose: () => {} }));

// A minimal ComposeBox stand-in that records replaceRange calls.
const replaceRange = vi.fn();
const composeStub = {
  textValue: () => '/model',
  caretOffset: () => 6,
  replaceRange,
  focus: () => {},
};
vi.mock('../core/state', () => ({ getCompose: () => composeStub }));

import { refreshSlashSuggest, slashKeydown, isSlashOpen, hideSlashSuggest } from './slash';

function popover(): HTMLElement {
  let el = document.getElementById('slash-popover');
  if (!el) {
    el = document.createElement('div');
    el.id = 'slash-popover';
    document.body.appendChild(el);
  }
  return el;
}

function activeLabel(): string | null {
  const active = popover().querySelector('.slash-item.is-active strong');
  return active?.textContent ?? null;
}

function key(name: string): KeyboardEvent {
  return new KeyboardEvent('keydown', { key: name, bubbles: true, cancelable: true });
}

describe('slash keyboard navigation', () => {
  beforeEach(async () => {
    replaceRange.mockClear();
    popover().innerHTML = '';
    hideSlashSuggest();
    await refreshSlashSuggest();
  });

  it('renders provider suggestions for /model and highlights the first', () => {
    expect(isSlashOpen()).toBe(true);
    expect(popover().querySelectorAll('.slash-item').length).toBe(2);
    expect(activeLabel()).toBe('Model: minimax-free');
  });

  it('ArrowDown moves the highlight and wraps around', () => {
    slashKeydown(key('ArrowDown'));
    expect(activeLabel()).toBe('Model: kimi');
    slashKeydown(key('ArrowDown')); // wrap back to first
    expect(activeLabel()).toBe('Model: minimax-free');
  });

  it('ArrowUp wraps to the last item', () => {
    slashKeydown(key('ArrowUp'));
    expect(activeLabel()).toBe('Model: kimi');
  });

  it('Tab autocompletes the active suggestion prefix', () => {
    const handled = slashKeydown(key('Tab'));
    expect(handled).toBe(true);
    expect(replaceRange).toHaveBeenCalledWith(0, 6, '/model minimax-free');
  });

  it('Enter autocompletes after navigating', () => {
    slashKeydown(key('ArrowDown'));
    slashKeydown(key('Enter'));
    expect(replaceRange).toHaveBeenCalledWith(0, 6, '/model kimi');
  });

  it('Escape closes the popover and consumes the event', () => {
    const handled = slashKeydown(key('Escape'));
    expect(handled).toBe(true);
    expect(isSlashOpen()).toBe(false);
  });

  it('does not consume keys when the popover is closed', () => {
    hideSlashSuggest();
    expect(slashKeydown(key('Enter'))).toBe(false);
    expect(slashKeydown(key('Tab'))).toBe(false);
  });
});
