// SapaLOQ widget entry point. This module is intentionally thin: it renders the
// app template, wires the DOM event listeners to the domain modules, and kicks
// off the bootstrap sequence (history restore, context usage, ping loop). All
// behaviour lives in the focused modules imported below.
import './style.css';
import { ComposeBox } from './ui/compose';
import { APP_TEMPLATE } from './ui/template';
import { getComposeInput } from './ui/dom';
import { ingestComposePaste } from './features/compose-paste';
import { addFiles } from './features/attachments';
import { getCompose, setCompose, isSubmitting } from './core/state';
import {
  cyclePanelSize,
  initWindowLayout,
  isExpanded,
  setExpanded,
  toggleExpanded,
} from './ui/window-layout';
import { cycleRingState, refreshUsage, runPing, startPingLoop } from './features/connection';
import { autosizeCompose, toggleComposeExpand } from './ui/compose-ui';
import { initComposeContextMenu } from './ui/compose-context-menu';
import { closeMessageMenu } from './features/messages';
import { refreshSlashSuggest, slashKeydown } from './features/slash';
import { initDragAndDrop } from './features/drag-overlay';
import { initChatController, stopActiveResponse, submitMessage } from './features/chat-controller';
import {
  closeHistoryMenu,
  isHistoryMenuOpen,
  loadSessionList,
  restoreChatHistory,
  startNewSession,
  switchSession,
  deleteSessionRoom,
  toggleHistoryMenu,
} from './features/history';
import { startRuntimeStatusLoop } from './features/runtime-status';
import { initWorkspacePicker } from './ui/workspace-picker';

document.querySelector('#app')!.innerHTML = APP_TEMPLATE;
initWorkspacePicker();

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

// --- Chat history switcher ------------------------------------------------

document.getElementById('btn-history')?.addEventListener('click', (e) => {
  e.stopPropagation();
  void toggleHistoryMenu();
});
document.getElementById('btn-new-chat')?.addEventListener('click', (e) => {
  e.stopPropagation();
  void startNewSession();
});
document.getElementById('history-new')?.addEventListener('click', (e) => {
  e.stopPropagation();
  void startNewSession();
});
document.getElementById('history-list')?.addEventListener('click', (e) => {
  const target = e.target as HTMLElement | null;
  const deleteBtn = target?.closest<HTMLElement>('.history-item-delete');
  if (deleteBtn) {
    e.stopPropagation();
    void deleteSessionRoom(deleteBtn.dataset.sessionId || '');
    return;
  }
  const item = target?.closest<HTMLElement>('.history-item-body');
  if (!item) return;
  const row = item.closest<HTMLElement>('.history-item');
  e.stopPropagation();
  void switchSession(row?.dataset.sessionId || '');
});
// Close the dropdown on any outside click.
document.addEventListener('click', (event) => {
  if (!isHistoryMenuOpen()) return;
  const target = event.target as HTMLElement | null;
  if (target?.closest('#history-menu') || target?.closest('#btn-history')) return;
  closeHistoryMenu();
});
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
    onKeyDown: (e) => slashKeydown(e),
    onPaste: (clipboard) => {
      const compose = getCompose();
      return compose ? ingestComposePaste(compose, clipboard) : false;
    },
  }));
  initComposeContextMenu(composeEl);
}
document.getElementById('compose-expand')?.addEventListener('click', () => toggleComposeExpand());
document.addEventListener('click', (event) => {
  const target = event.target as HTMLElement | null;
  if (!target?.closest('.message-menu') && !target?.closest('.message--user')) closeMessageMenu();
});

document.addEventListener('paste', (event) => {
  if (document.activeElement?.id === 'compose-input') return;
  event.preventDefault();
  const compose = getCompose();
  if (!compose) return;
  void ingestComposePaste(compose, (event as ClipboardEvent).clipboardData).then((handled) => {
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

// WebKitGTK can freeze controls created inside a display:none popup at 0x0.
// Hydrate the transcript only after the first real expansion, when the window
// and message-list have measurable dimensions. Retry on a later expansion if
// the core was not ready yet.
let visibleHistoryHydrated = false;
let visibleHistoryHydrating = false;
window.addEventListener('sapaloq:expanded', () => {
  if (visibleHistoryHydrated || visibleHistoryHydrating) return;
  visibleHistoryHydrating = true;
  void restoreChatHistory().then((ok) => {
    visibleHistoryHydrated = ok;
    visibleHistoryHydrating = false;
  });
});
void loadSessionList();
// Best-effort initial context-usage read. If core isn't ready yet (race on
// fresh open), this fails silently and refreshUsage() on the first successful
// ping + the periodic usage timer fill the pill instead of leaving "0/0".
void refreshUsage();
startPingLoop();
startRuntimeStatusLoop();
