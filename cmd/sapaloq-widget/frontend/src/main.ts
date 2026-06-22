// SapaLOQ widget entry point. This module is intentionally thin: it renders the
// app template, wires the DOM event listeners to the domain modules, and kicks
// off the bootstrap sequence (history restore, context usage, ping loop). All
// behaviour lives in the focused modules imported below.
import './style.css';
import { ContextUsage } from '../wailsjs/go/main/App';
import type { ChatUsage } from './core/types';
import { ComposeBox } from './ui/compose';
import { APP_TEMPLATE } from './ui/template';
import { getComposeInput } from './ui/dom';
import { setCompose, isSubmitting } from './core/state';
import {
  cyclePanelSize,
  initWindowLayout,
  isExpanded,
  setExpanded,
  toggleExpanded,
} from './ui/window-layout';
import { cycleRingState, renderUsage, runPing, startPingLoop } from './features/connection';
import { autosizeCompose, toggleComposeExpand } from './ui/compose-ui';
import { closeMessageMenu } from './features/messages';
import { refreshSlashSuggest } from './features/slash';
import { addClipboardItems, addFiles } from './features/attachments';
import { initDragAndDrop } from './features/drag-overlay';
import { initChatController, stopActiveResponse, submitMessage } from './features/chat-controller';
import { restoreChatHistory } from './features/history';
import { startRuntimeStatusLoop } from './features/runtime-status';

document.querySelector('#app')!.innerHTML = APP_TEMPLATE;

void initWindowLayout();

// --- Orb + panel controls -------------------------------------------------

let clickTimer: ReturnType<typeof setTimeout> | null = null;
document.getElementById('orb')?.addEventListener('click', (e) => {
  e.stopPropagation();
  if (e.altKey) {
    void runPing();
    return;
  }
  if (clickTimer) return;
  clickTimer = setTimeout(() => {
    clickTimer = null;
    void toggleExpanded();
  }, 200);
});
document.getElementById('btn-close')?.addEventListener('click', () => void setExpanded(false));
document.getElementById('btn-resize')?.addEventListener('click', () => void cyclePanelSize());
document.getElementById('orb')?.addEventListener('dblclick', (e) => {
  e.preventDefault();
  if (clickTimer) {
    clearTimeout(clickTimer);
    clickTimer = null;
  }
  if (!isExpanded()) cycleRingState();
});
document.getElementById('send-btn')?.addEventListener('click', () => {
  if (isSubmitting()) void stopActiveResponse();
  else void submitMessage();
});
document.getElementById('attach-btn')?.addEventListener('click', () => {
  const input = document.getElementById('attach-input') as HTMLInputElement | null;
  input?.click();
});
document.getElementById('attach-input')?.addEventListener('change', (event) => {
  const input = event.currentTarget as HTMLInputElement;
  if (input.files?.length) void addFiles(input.files);
  input.value = '';
});

// --- Compose box ----------------------------------------------------------

const composeEl = getComposeInput();
if (composeEl) {
  setCompose(new ComposeBox(composeEl, {
    onChange: () => { autosizeCompose(); void refreshSlashSuggest(); },
    onSubmit: () => void submitMessage(),
  }));
}
document.getElementById('compose-expand')?.addEventListener('click', () => toggleComposeExpand());
// File paste is handled here (ComposeBox lets file pastes through); plain-text
// paste is normalised inside ComposeBox.
composeEl?.addEventListener('paste', (event) => {
  const clipboard = (event as ClipboardEvent).clipboardData;
  const hasFile = Array.from(clipboard?.items || []).some((item) => item.kind === 'file');
  if (hasFile) {
    event.preventDefault();
    void addClipboardItems(clipboard);
  }
});
document.addEventListener('click', (event) => {
  const target = event.target as HTMLElement | null;
  if (!target?.closest('.message-menu') && !target?.closest('.message--user')) closeMessageMenu();
});

document.addEventListener('paste', (event) => {
  if (document.activeElement?.id === 'compose-input') return;
  const clipboard = (event as ClipboardEvent).clipboardData;
  if (Array.from(clipboard?.items || []).some((item) => item.kind === 'file')) event.preventDefault();
  void addClipboardItems(clipboard).then((handled) => {
    if (handled) {
      void setExpanded(true);
      document.getElementById('compose-input')?.focus();
    }
  });
});

// --- Drag-and-drop + live stream ------------------------------------------

initChatController();
initDragAndDrop();

// --- Bootstrap ------------------------------------------------------------

void restoreChatHistory();
void ContextUsage().then((usage) => renderUsage(usage as ChatUsage)).catch(() => undefined);
startPingLoop();
startRuntimeStatusLoop();
