// Drag-and-drop file overlay wiring. Handles both the native Wails path-drop
// (WebKitGTK, GTK-level) and the HTML File-object drop fallback (Chromium /
// browser preview / in-webview image drags), plus the overlay highlight
// lifecycle with the safety-net idle timer.
import { OnFileDrop } from '../../wailsjs/runtime/runtime';
import { isExpanded, setExpanded } from '../ui/window-layout';
import { addDroppedPaths, addFiles, collectTransferFiles } from './attachments';

// Highlight helpers shared by native (OnFileDrop) and HTML drag paths.
let dragDepth = 0;
// Safety-net: a drag that merely passes *over* SapaLOQ and is dropped on another
// app often gives us no terminating event (no drop here, an unreliable final
// dragleave, and no dragend for external sources). We therefore (re)arm a timer
// on every dragover; if no further dragover fires the drag has left our window,
// so we force-clear the overlay. dragover fires continuously (~tens of ms) while
// a drag hovers, so this only trips once the pointer is truly gone.
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
function armDragIdleTimer() {
  clearDragIdleTimer();
  dragIdleTimer = setTimeout(() => hideDragOverlay(true), 220);
}
function showDragOverlay() {
  dragDepth++;
  getPopup()?.classList.add('is-dragging-file');
  armDragIdleTimer();
}
function hideDragOverlay(force = false) {
  if (force) dragDepth = 0;
  else dragDepth = Math.max(0, dragDepth - 1);
  if (dragDepth === 0) {
    clearDragIdleTimer();
    getPopup()?.classList.remove('is-dragging-file');
  }
}

export function initDragAndDrop() {
  const popup = getPopup();

  // Native file drop (Wails). On WebKitGTK the webview drag events are disabled
  // (DisableWebViewDrop:true in main.go), so the only way to receive drops from
  // the file manager / desktop is this GTK-level callback, which hands us file
  // *paths*. Listen on the whole native window: target-scoped drops become
  // unreliable after the GTK input shape switches between orb and panel.
  try {
    OnFileDrop((_x, _y, paths) => {
      if (paths?.length) {
        hideDragOverlay(true);
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
  popup?.addEventListener('dragenter', (event) => {
    event.preventDefault();
    showDragOverlay();
  });
  popup?.addEventListener('dragover', (event) => {
    event.preventDefault();
    if (!popup.classList.contains('is-dragging-file')) showDragOverlay();
    else armDragIdleTimer();
  });
  popup?.addEventListener('dragleave', (event) => {
    // Only count leaves that actually exit the popup rect, not child crossings.
    const r = popup.getBoundingClientRect();
    if (event.clientX <= r.left || event.clientX >= r.right || event.clientY <= r.top || event.clientY >= r.bottom) {
      hideDragOverlay();
    }
  });
  popup?.addEventListener('drop', (event) => {
    event.preventDefault();
    hideDragOverlay(true);
    const transfer = (event as DragEvent).dataTransfer;
    const files = collectTransferFiles(transfer);
    if (files.length) void addFiles(files);
  });
  // Document-level fallback so the overlay still shows when the popup is
  // collapsed (pointer-events:none on #popup blocks its own dragover).
  document.addEventListener('dragover', (event) => {
    if (!document.getElementById('popup')) return;
    event.preventDefault();
    if (popup?.classList.contains('is-dragging-file')) armDragIdleTimer();
    else showDragOverlay();
  });
  document.addEventListener('drop', (event) => {
    if (!popup?.classList.contains('is-dragging-file')) return;
    event.preventDefault();
    hideDragOverlay(true);
    const transfer = (event as DragEvent).dataTransfer;
    const files = collectTransferFiles(transfer);
    if (files.length) void addFiles(files);
  });
  // Force-clear when the drag leaves the window entirely (dropped on another app
  // or escaped past the edge). A leave to a null relatedTarget, or to coordinates
  // at/outside the viewport bounds, means the pointer is no longer over us.
  document.addEventListener('dragleave', (event) => {
    const drag = event as DragEvent;
    const outside = drag.relatedTarget === null
      || drag.clientX <= 0 || drag.clientY <= 0
      || drag.clientX >= window.innerWidth || drag.clientY >= window.innerHeight;
    if (outside) hideDragOverlay(true);
  });
  // dragend fires on in-webview drag sources regardless of where the drop landed.
  window.addEventListener('dragend', () => hideDragOverlay(true));
}
