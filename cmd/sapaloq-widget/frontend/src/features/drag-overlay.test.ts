import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// drag-overlay imports the Wails runtime + attachment helpers. Neither matters
// for the overlay-flicker behaviour under test, so stub them out.
vi.mock('../../wailsjs/runtime/runtime', () => ({ OnFileDrop: vi.fn() }));
vi.mock('../ui/window-layout', () => ({ isExpanded: () => true, setExpanded: vi.fn() }));
vi.mock('./attachments', () => ({ addDroppedPaths: vi.fn(), addFiles: vi.fn(), collectTransferFiles: () => [] }));

import { initDragAndDrop } from './drag-overlay';

function dispatchDragOver() {
  // jsdom's DragEvent constructor is limited; a plain Event with the right type
  // is enough since the handler only reads .dataTransfer (we don't need files).
  document.dispatchEvent(new Event('dragover', { bubbles: true, cancelable: true }));
}

function dispatchChildDragLeave(popup: HTMLElement) {
  // The old flicker trigger: a dragleave bubbling up while the pointer is still
  // inside the window. With the new code there is no dragleave listener at all,
  // so this must be a no-op for the overlay.
  popup.dispatchEvent(new Event('dragleave', { bubbles: true }));
}

function isShown(popup: HTMLElement) {
  return popup.classList.contains('is-dragging-file');
}

describe('drag overlay - no flicker while a file is held over the widget', () => {
  let popup: HTMLElement;

  beforeEach(() => {
    vi.useFakeTimers();
    document.body.innerHTML = '<div id="popup"></div>';
    popup = document.getElementById('popup') as HTMLElement;
    initDragAndDrop();
  });

  afterEach(() => {
    vi.runOnlyPendingTimers();
    vi.useRealTimers();
    document.body.innerHTML = '';
  });

  it('stays continuously shown across a long held drag (no blink)', () => {
    // Simulate 30 dragover ticks ~16ms apart, with child dragleave noise in
    // between - the exact pattern that previously toggled the class repeatedly.
    // The class must never blink off mid-drag: it is checked on every tick.
    for (let i = 0; i < 30; i++) {
      dispatchDragOver();
      dispatchChildDragLeave(popup);
      expect(isShown(popup)).toBe(true);
      vi.advanceTimersByTime(16); // shorter than the 180ms idle timeout
    }
    expect(isShown(popup)).toBe(true);
  });

  it('child dragleave noise alone never hides the overlay', () => {
    dispatchDragOver();
    expect(isShown(popup)).toBe(true);
    for (let i = 0; i < 10; i++) dispatchChildDragLeave(popup);
    // dragleave is not even listened to => still shown.
    expect(isShown(popup)).toBe(true);
  });

  it('clears the overlay after the drag leaves (idle timeout fires)', () => {
    dispatchDragOver();
    expect(isShown(popup)).toBe(true);
    // No more dragover => idle timer (180ms) trips and removes the class.
    vi.advanceTimersByTime(200);
    expect(isShown(popup)).toBe(false);
  });

  it('re-arms (stays shown) while dragover keeps arriving under the idle window', () => {
    dispatchDragOver();
    vi.advanceTimersByTime(100);
    dispatchDragOver(); // before the 180ms idle => stays shown
    expect(isShown(popup)).toBe(true);
    vi.advanceTimersByTime(100);
    dispatchDragOver();
    expect(isShown(popup)).toBe(true);
  });
});
