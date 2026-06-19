import {
  ScreenGetAll,
  WindowGetPosition,
  WindowSetPosition,
  WindowSetSize,
} from '../wailsjs/runtime/runtime';
import { SyncInputShape } from '../wailsjs/go/main/App';

export const COLLAPSED = { w: 76, h: 76 };
export const EXPANDED = { w: 376, h: 536 };
const MARGIN = 20;
const PANEL_TRANSITION_MS = 230;

let expanded = false;
let layoutReady = false;

function nextFrame() {
  return new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
}

function delay(ms: number) {
  return new Promise<void>((resolve) => setTimeout(resolve, ms));
}

export function isExpanded() {
  return expanded;
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
  if (next === expanded) return;

  const pos = await WindowGetPosition();
  const deltaH = EXPANDED.h - COLLAPSED.h;
  const popup = document.getElementById('popup');

  if (next) {
    WindowSetSize(EXPANDED.w, EXPANDED.h);
    WindowSetPosition(pos.x, pos.y - deltaH);
    expanded = true;
    document.body.classList.add('opening');
    document.body.classList.add('expanded');
    await nextFrame();
    document.body.classList.remove('opening');
    popup?.setAttribute('aria-hidden', 'false');
    SyncInputShape(false);
    return;
  }

  expanded = false;
  document.body.classList.add('closing');
  document.body.classList.remove('expanded');
  popup?.setAttribute('aria-hidden', 'true');
  SyncInputShape(true);
  await delay(PANEL_TRANSITION_MS);
  WindowSetSize(COLLAPSED.w, COLLAPSED.h);
  WindowSetPosition(pos.x, pos.y + deltaH);
  document.body.classList.remove('closing');
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
