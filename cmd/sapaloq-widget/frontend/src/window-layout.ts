import {
  ScreenGetAll,
  WindowGetPosition,
  WindowSetPosition,
  WindowSetSize,
} from '../wailsjs/runtime/runtime';
import { SyncInputShape } from '../wailsjs/go/main/App';

export const COLLAPSED = { w: 48, h: 48 };
export const EXPANDED = { w: 360, h: 520 };
const MARGIN = 20;

let expanded = false;

export function isExpanded() {
  return expanded;
}

export async function placeBottomLeft(size = COLLAPSED) {
  const screens = await ScreenGetAll();
  const screen = screens.find((s) => s.isCurrent) ?? screens.find((s) => s.isPrimary) ?? screens[0];
  if (!screen) return;

  const x = MARGIN;
  const y = screen.height - size.h - MARGIN;
  WindowSetSize(size.w, size.h);
  WindowSetPosition(x, y);
}

export async function setExpanded(next: boolean) {
  if (next === expanded) return;

  const pos = await WindowGetPosition();
  const deltaH = EXPANDED.h - COLLAPSED.h;

  if (next) {
    WindowSetSize(EXPANDED.w, EXPANDED.h);
    WindowSetPosition(pos.x, pos.y - deltaH);
  } else {
    WindowSetSize(COLLAPSED.w, COLLAPSED.h);
    WindowSetPosition(pos.x, pos.y + deltaH);
  }

  expanded = next;
  document.body.classList.toggle('expanded', next);
  const popup = document.getElementById('popup');
  popup?.setAttribute('aria-hidden', next ? 'false' : 'true');
  SyncInputShape(!next);
}

export async function toggleExpanded() {
  await setExpanded(!expanded);
}

export async function initWindowLayout() {
  await placeBottomLeft(COLLAPSED);
  document.body.classList.remove('expanded');
}
