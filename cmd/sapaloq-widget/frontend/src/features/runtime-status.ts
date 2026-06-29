import { RuntimeStatus } from '../../wailsjs/go/main/App';
import type { ActorRuntimeStatus, RuntimeStatus as RuntimeStatusData } from '../core/types';
import { getSessionID, setSessionID } from '../core/state';
import { openTaskMonitor } from '../ui/task-monitor-overlay';

// Per chat-room workspace cache. Never reuse another session's path when the
// active room has no persisted cwd yet (switch room / restart race).
const workspaceBySession = new Map<string, string>();

function shortPath(path: string) {
  const home = path.match(/^\/home\/[^/]+/i)?.[0];
  return home ? path.replace(home, '~') : path;
}

function roleLabel(role: string) {
  if (role === 'task-runner') return 'Agent';
  if (role === 'planner') return 'Planner';
  if (role === 'scribe') return 'Scribe';
  return role || 'Actor';
}

function isSettled(actor: ActorRuntimeStatus) {
  const status = (actor.status || '').toLowerCase();
  const phase = (actor.phase || '').toLowerCase();
  return status === 'done' || status === 'failed' || status === 'stopped' ||
    phase === 'finalizing' || phase === 'exited';
}

export function actorState(actor?: ActorRuntimeStatus) {
  if (!actor) return 'idle';
  if (actor.status === 'failed' || actor.status === 'stopped') return actor.status;
  if (isSettled(actor)) return 'idle';
  return 'active';
}

function actorTile(role: string, actor?: ActorRuntimeStatus) {
  const article = document.createElement('article');
  article.className = 'actor-tile';
  article.dataset.role = role;
  if (actor?.id) article.dataset.taskId = actor.id;
  article.dataset.state = actorState(actor);
  article.title = actor ? `${actor.id}\n${actor.workspace}` : `${roleLabel(role)} tidak aktif`;

  if (role === 'planner' || role === 'task-runner') {
    article.classList.add('actor-tile--clickable');
    article.setAttribute('role', 'button');
    article.setAttribute('tabindex', '0');
    const tab = role === 'task-runner' ? 'agent' : 'planner';
    const open = () => void openTaskMonitor({ tab: tab as 'planner' | 'agent' });
    article.addEventListener('click', open);
    article.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); open(); }
    });
  }

  const signal = document.createElement('span');
  signal.className = 'actor-signal';
  const copy = document.createElement('span');
  const label = document.createElement('b');
  label.textContent = roleLabel(role);
  const phase = document.createElement('small');
  phase.textContent = actor && !isSettled(actor) ? (actor.phase || actor.status || 'active') : 'idle';
  copy.append(label, phase);
  article.append(signal, copy);
  return article;
}

let workspaceCardSessionId = '';

export function applyWorkspacePath(path: string, sessionID?: string) {
  const cleaned = path.trim();
  if (!cleaned) return;
  const sid = (sessionID || getSessionID()).trim();
  if (sid) workspaceBySession.set(sid, cleaned);
  const active = getSessionID().trim();
  if (active && sid && sid !== active) return;
  const workspace = document.getElementById('runtime-workspace');
  const workspaceText = workspace?.querySelector('strong');
  if (workspace) {
    workspace.title = cleaned;
    workspace.dataset.workspacePath = cleaned;
  }
  if (workspaceText) workspaceText.textContent = shortPath(cleaned);
  if (sid) workspaceCardSessionId = sid;
}

function activeWorkspacePath(status: RuntimeStatusData) {
  const sid = (status.session_id || getSessionID()).trim();
  const fromCore = (status.session_workspace || '').trim();
  if (fromCore && sid) workspaceBySession.set(sid, fromCore);
  if (fromCore) return fromCore;
  if (sid && workspaceBySession.has(sid)) return workspaceBySession.get(sid)!;
  if (sid) return '';
  return status.workspace_path || '';
}

export function renderRuntimeStatus(status: RuntimeStatusData) {
  if (status.session_id) setSessionID(status.session_id);

  const model = document.getElementById('runtime-model-name');
  const provider = document.getElementById('runtime-provider');
  if (model) {
    model.textContent = status.model || 'default';
    model.title = `${status.driver} · ${status.reasoning || 'default reasoning'}`;
  }
  if (provider) provider.textContent = status.provider || status.driver || 'provider';

  const actors = document.getElementById('runtime-actors');
  if (actors) {
    const planner = status.actors.find((actor) => actor.role === 'planner');
    const agent = status.actors.find((actor) => actor.role === 'task-runner');
    const others = status.actors.filter((actor) => actor.role !== 'planner' && actor.role !== 'task-runner');
    actors.replaceChildren(actorTile('planner', planner), actorTile('task-runner', agent), ...others.map((actor) => actorTile(actor.role, actor)));
  }

  const workspace = document.getElementById('runtime-workspace');
  const workspaceText = workspace?.querySelector('strong');
  const activeWorkspace = activeWorkspacePath(status);
  const sid = (status.session_id || getSessionID()).trim();
  if (workspace) {
    if (activeWorkspace) {
      workspace.title = activeWorkspace;
      workspace.dataset.workspacePath = activeWorkspace;
      if (workspaceText) workspaceText.textContent = shortPath(activeWorkspace);
      workspaceCardSessionId = sid;
    } else if (sid && workspaceCardSessionId && workspaceCardSessionId !== sid) {
      const fallback = (status.workspace_path || '').trim();
      workspace.title = fallback;
      workspace.dataset.workspacePath = fallback;
      if (workspaceText && fallback) workspaceText.textContent = shortPath(fallback);
      workspaceCardSessionId = sid;
    }
  }
}

export function currentSessionWorkspacePath(): string {
  const el = document.getElementById('runtime-workspace');
  return (el?.dataset.workspacePath || '').trim();
}

export async function refreshRuntimeStatus() {
  try {
    renderRuntimeStatus(await RuntimeStatus() as RuntimeStatusData);
  } catch {
    // ping loop owns offline feedback; preserve the last snapshot.
  }
}

/** One-shot refresh when the panel opens; ongoing polls piggyback on ping (see connection.ts). */
export function startRuntimeStatusLoop() {
  void refreshRuntimeStatus();
}
