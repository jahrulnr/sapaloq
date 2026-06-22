import {
  ScreenGetAll,
  WindowGetPosition,
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
      document.body.classList.remove('closing');
      document.body.classList.add('opening');
      document.body.classList.add('expanded');
      await nextFrame();
      document.body.classList.remove('opening');
      popup?.setAttribute('aria-hidden', 'false');
      SyncInputShape(false);
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
