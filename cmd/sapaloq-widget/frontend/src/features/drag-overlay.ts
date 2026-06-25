// Drag-and-drop file overlay wiring. Handles both the native Wails path-drop
// (WebKitGTK, GTK-level) and the HTML File-object drop fallback (Chromium /
// browser preview / in-webview image drags), plus the overlay highlight
// lifecycle with the safety-net idle timer.
import { OnFileDrop } from '../../wailsjs/runtime/runtime';
import { isExpanded, setExpanded } from '../ui/window-layout';
import { addDroppedPaths, addFiles, collectTransferFiles } from './attachments';

// Overlay state is a single boolean driven purely by a re-armed idle timer.
//
// The old approach toggled the overlay class via a dragenter/dragleave depth
// counter. On WebKitGTK that flickered ("blink"): while a file is *held* over
// the widget, dragover fires continuously and dragleave fires on every child
// crossing - frequently with relatedTarget === null - so the class was removed
// and re-added many times a second. We instead show the overlay once on the
// first dragover and keep it up as long as dragover keeps arriving; a single
// idle timer (re-armed on every dragover) clears it once the drag has truly
// left the window. No dragleave-driven hiding => no flicker.
let dragActive = false;
let dragIdleTimer: ReturnType<typeof setTimeout> | null = null;

function getPopup() {
  return document.getElementById('popup');
}

function clearDragIdleTimer() {
  if (dragIdleTimer !== null) {
    clearTimeout(dragIdleTimer);
    dragIdleTimer = null;
  }
}

// markDragOver is the single entry point for "a drag is currently over us".
// It shows the overlay exactly once and (re)arms the idle timer. dragover fires
// every few tens of ms while a drag hovers, so the timer only trips once the
// pointer is genuinely gone (dropped elsewhere / escaped the edge).
function markDragOver() {
  if (!dragActive) {
    dragActive = true;
    getPopup()?.classList.add('is-dragging-file');
  }
  clearDragIdleTimer();
  dragIdleTimer = setTimeout(hideDragOverlay, 180);
}

function hideDragOverlay() {
  clearDragIdleTimer();
  if (!dragActive) return;
  dragActive = false;
  getPopup()?.classList.remove('is-dragging-file');
}

export function initDragAndDrop() {
  // Native file drop (Wails). On WebKitGTK the webview drag events are disabled
  // (DisableWebViewDrop:true in main.go), so the only way to receive drops from
  // the file manager / desktop is this GTK-level callback, which hands us file
  // *paths*. Listen on the whole native window: target-scoped drops become
  // unreliable after the GTK input shape switches between orb and panel.
  try {
    OnFileDrop((_x, _y, paths) => {
      if (paths?.length) {
        hideDragOverlay();
        if (!isExpanded()) void setExpanded(true);
        void addDroppedPaths(paths);
        document.getElementById('compose-input')?.focus();
      }
    }, false);
  } catch {
    // OnFileDrop only exists inside a Wails runtime; ignore in plain browser.
  }

  // HTML drag fallback for environments where the webview *does* deliver File
  // objects (Chromium, browser preview, in-webview image drags). WebKitGTK with
  // DisableWebViewDrop:true will never reach these, so there is no conflict.
  //
  // We listen only at document level (capture not needed): a single dragover
  // handler covers both the expanded panel and the collapsed orb (where
  // #popup has pointer-events:none and would never see its own dragover). No
  // dragenter/dragleave listeners - those are what caused the flicker.
  const onDragOver = (event: Event) => {
    if (!document.getElementById('popup')) return;
    event.preventDefault();
    markDragOver();
  };
  document.addEventListener('dragover', onDragOver);

  const onDrop = (event: Event) => {
    hideDragOverlay();
    event.preventDefault();
    const transfer = (event as DragEvent).dataTransfer;
    const files = collectTransferFiles(transfer);
    if (files.length) void addFiles(files);
  };
  document.addEventListener('drop', onDrop);

  // dragend fires on in-webview drag sources regardless of where the drop
  // landed; a final safety-net clear in case the idle timer hasn't tripped yet.
  window.addEventListener('dragend', hideDragOverlay);
}
