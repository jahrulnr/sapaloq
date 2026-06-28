// WORKSPACE card → native folder dialog (GTK/Nautilus on GNOME), then persist via core.

import { PickWorkspaceFolder, RuntimeStatus, SetWorkspace } from '../../wailsjs/go/main/App';
import { getSessionID, setSessionID } from '../core/state';
import { applyWorkspacePath, refreshRuntimeStatus } from '../features/runtime-status';

async function resolveSessionID() {
  const local = getSessionID().trim();
  if (local) return local;
  try {
    const status = await RuntimeStatus();
    const id = (status.session_id || '').trim();
    if (id) setSessionID(id);
    return id;
  } catch {
    return '';
  }
}

async function chooseWorkspace(startDir: string) {
  const picked = (await PickWorkspaceFolder(startDir)).trim();
  if (!picked) return;
  const sessionID = await resolveSessionID();
  if (!sessionID) return;
  const res = await SetWorkspace(sessionID, picked);
  if (!res?.ok) return;
  const path = (res.path || picked).trim();
  const sid = (res.session_id || sessionID).trim();
  applyWorkspacePath(path, sid);
  if (res.session_id) setSessionID(res.session_id);
  void refreshRuntimeStatus();
}

export function wireWorkspaceCard(el: HTMLElement) {
  if (el.dataset.workspaceWired === '1') return;
  el.dataset.workspaceWired = '1';
  el.classList.add('runtime-workspace--clickable');
  if (el.tagName !== 'BUTTON') {
    el.setAttribute('role', 'button');
    el.setAttribute('tabindex', '0');
  }
  if (!el.getAttribute('aria-label')) el.setAttribute('aria-label', 'Pilih workspace');
  const open = (event: Event) => {
    event.preventDefault();
    event.stopPropagation();
    const start = el.dataset.workspacePath || '';
    void chooseWorkspace(start);
  };
  el.addEventListener('click', open);
  el.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      void chooseWorkspace(el.dataset.workspacePath || '');
    }
  });
}

export function initWorkspacePicker() {
  const el = document.getElementById('runtime-workspace');
  if (el) wireWorkspaceCard(el);
}
