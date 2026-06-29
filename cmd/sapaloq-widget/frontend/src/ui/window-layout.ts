import {
  ScreenGetAll,
  WindowGetPosition,
  WindowGetSize,
  WindowSetPosition,
  WindowSetSize,
} from '../../wailsjs/runtime/runtime';
import { SyncInputShape } from '../../wailsjs/go/main/App';

export const COLLAPSED = { w: 76, h: 76 };
export const PANEL_SIZES = [
  { w: 376, h: 640 },
  { w: 520, h: 760 },
  { w: 720, h: 860 },
];
export const EXPANDED = PANEL_SIZES[0];
const MARGIN = 20;
const PANEL_TRANSITION_MS = 230;

let expanded = false;
let layoutReady = false;
let transitionBusy = false;
let panelSizeIndex = 0;

function nextFrame() {
  return new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
}

function delay(ms: number) {
  return new Promise<void>((resolve) => setTimeout(resolve, ms));
}

// waitForWindowSize blocks until the OS window has actually reached (within a
// few px) the requested size, or a short timeout elapses. WindowSetSize is a
// non-blocking runtime call: the OS may not have resized the window yet when
// the CSS `.expanded` class is applied, so the panel (which fills the window:
// `.popup` is width/height 100%) gets painted into a still-collapsed window and
// shows up clipped. Gating the visual switch on the confirmed size removes that
// race; the timeout guarantees we never hang if the runtime stops reporting.
async function waitForWindowSize(target: { w: number; h: number }, timeoutMs = 300) {
  const tolerance = 4;
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    try {
      const size = await WindowGetSize();
      if (Math.abs(size.w - target.w) <= tolerance && Math.abs(size.h - target.h) <= tolerance) {
        return;
      }
    } catch {
      return; // runtime unavailable (plain browser) - don't block the UI
    }
    if (Date.now() >= deadline) return;
    await nextFrame();
  }
}

export function isExpanded() {
  return expanded;
}

export function currentPanelSize() {
  return PANEL_SIZES[panelSizeIndex];
}

export async function placeBottomLeft(size = COLLAPSED) {
  try {
    const screens = await ScreenGetAll();
    const screen = screens.find((s) => s.isCurrent) ?? screens.find((s) => s.isPrimary) ?? screens[0];
    if (!screen) return;
    const x = MARGIN;
    const y = screen.height - size.h - MARGIN;
    WindowSetSize(size.w, size.h);
    WindowSetPosition(x, y);
  } catch {
    // ScreenGetAll not ready yet; retry on next tick
    setTimeout(() => void placeBottomLeft(size), 250);
  }
}

export async function setExpanded(next: boolean) {
  if (next === expanded || transitionBusy) return;
  transitionBusy = true;

  const pos = await WindowGetPosition();
  const expandedSize = currentPanelSize();
  const deltaH = expandedSize.h - COLLAPSED.h;
  const popup = document.getElementById('popup');

  try {
    if (next) {
      WindowSetSize(expandedSize.w, expandedSize.h);
      WindowSetPosition(pos.x, pos.y - deltaH);
      expanded = true;
      // Wait until the OS window has actually grown before painting the panel
      // at full size, otherwise it renders clipped inside the still-collapsed
      // window (the intermittent "panel kepotong" bug). Bounded by a timeout so
      // a non-reporting runtime can never wedge the open animation.
      await waitForWindowSize(expandedSize);
      document.body.classList.remove('closing');
      document.body.classList.add('opening');
      document.body.classList.add('expanded');
      await nextFrame();
      document.body.classList.remove('opening');
      popup?.setAttribute('aria-hidden', 'false');
      SyncInputShape(false);
      window.dispatchEvent(new Event('sapaloq:expanded'));
      return;
    }

    expanded = false;
    document.body.classList.remove('opening');
    document.body.classList.add('closing');
    document.body.classList.remove('expanded');
    popup?.setAttribute('aria-hidden', 'true');
    SyncInputShape(true);
    await delay(PANEL_TRANSITION_MS);
    WindowSetSize(COLLAPSED.w, COLLAPSED.h);
    WindowSetPosition(pos.x, pos.y + deltaH);
    window.dispatchEvent(new Event('sapaloq:collapsed'));
  } finally {
    document.body.classList.remove('closing');
    transitionBusy = false;
  }
}

export async function cyclePanelSize() {
  if (!expanded || transitionBusy) return;
  const oldSize = currentPanelSize();
  panelSizeIndex = (panelSizeIndex + 1) % PANEL_SIZES.length;
  const nextSize = currentPanelSize();
  const pos = await WindowGetPosition();
  WindowSetSize(nextSize.w, nextSize.h);
  WindowSetPosition(pos.x, pos.y - (nextSize.h - oldSize.h));
}

export async function toggleExpanded() {
  await setExpanded(!expanded);
}

export async function initWindowLayout() {
  // Wait for the Wails window to be realized before resizing,
  // otherwise Wails can revert to its default 10x10 fallback.
  for (let i = 0; i < 20 && !layoutReady; i++) {
    await new Promise((r) => setTimeout(r, 100));
    try {
      const screens = await ScreenGetAll();
      if (screens && screens.length > 0) layoutReady = true;
    } catch {
      /* keep waiting */
    }
  }
  await placeBottomLeft(COLLAPSED);
  document.body.classList.remove('expanded');
}
