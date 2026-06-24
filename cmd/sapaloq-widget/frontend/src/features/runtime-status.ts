import { RuntimeStatus } from '../../wailsjs/go/main/App';
import type { ActorRuntimeStatus, RuntimeStatus as RuntimeStatusData } from '../core/types';

let timer: ReturnType<typeof setInterval> | null = null;

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

// A worker that has reached a terminal outcome (done/failed/stopped) or whose
// phase says it has wound down (finalizing/exited) is NOT live work - it must
// not keep the pill blinking. Only genuinely-running phases map to 'active'.
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
  article.dataset.state = actorState(actor);
  article.title = actor ? `${actor.id}\n${actor.workspace}` : `${roleLabel(role)} tidak aktif`;

  const signal = document.createElement('span');
  signal.className = 'actor-signal';
  const copy = document.createElement('span');
  const label = document.createElement('b');
  label.textContent = roleLabel(role);
  const phase = document.createElement('small');
  // Once settled, the pill reads 'idle' rather than freezing on a transient
  // phase like 'finalizing' that no longer reflects live work.
  phase.textContent = actor && !isSettled(actor) ? (actor.phase || actor.status || 'active') : 'idle';
  copy.append(label, phase);
  article.append(signal, copy);
  return article;
}

export function renderRuntimeStatus(status: RuntimeStatusData) {
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
  const activeWorkspace = status.actors.find((actor) => actor.role === 'task-runner')?.workspace ||
    status.actors.find((actor) => actor.role === 'planner')?.workspace ||
    status.workspace_path;
  if (workspace && activeWorkspace) workspace.title = activeWorkspace;
  if (workspaceText && activeWorkspace) workspaceText.textContent = shortPath(activeWorkspace);
}

export async function refreshRuntimeStatus() {
  try {
    renderRuntimeStatus(await RuntimeStatus() as RuntimeStatusData);
  } catch {
    // Connection indicator owns offline feedback; preserve the last snapshot.
  }
}

export function startRuntimeStatusLoop() {
  if (timer) return;
  void refreshRuntimeStatus();
  timer = setInterval(() => void refreshRuntimeStatus(), 3000);
}
