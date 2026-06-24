import { describe, it, expect } from 'vitest';
import { activeSlashAtChat } from './slash';

describe('activeSlashAtChat', () => {
  it('detects a slash command at the start of the input', () => {
    const got = activeSlashAtChat('/model', 6);
    expect(got).toEqual({ query: 'model', slashIndex: 0 });
  });

  it('returns null when there is no slash before the caret', () => {
    expect(activeSlashAtChat('hello world', 11)).toBeNull();
  });

  it('keeps the popover open for argument suggestions after a space', () => {
    // "/model minimax" - the space ends the command name but argument
    // suggestions (provider keys) should still resolve.
    const got = activeSlashAtChat('/model minimax', 14);
    expect(got).toEqual({ query: 'model minimax', slashIndex: 0 });
  });

  it('closes the popover once a newline is typed', () => {
    expect(activeSlashAtChat('/model\nmore', 11)).toBeNull();
  });

  it('only triggers on a slash at a word boundary', () => {
    // A slash glued to a preceding word (e.g. a path) is not a command.
    expect(activeSlashAtChat('path/model', 10)).toBeNull();
  });

  it('triggers on a slash after whitespace mid-text', () => {
    const got = activeSlashAtChat('look /reset', 11);
    expect(got).toEqual({ query: 'reset', slashIndex: 5 });
  });
});
