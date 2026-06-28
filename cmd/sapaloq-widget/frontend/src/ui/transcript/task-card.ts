import { renderMarkdown } from '../markdown';
import { setRingState } from '../../features/connection';
import { refreshRuntimeStatus } from '../../features/runtime-status';
import { restoreChatHistory } from '../../features/history';
import { taskBubbles, taskStatuses, getSessionID } from '../../core/state';
import { ResumeTask } from '../../../wailsjs/go/main/App';
import type { TranscriptEntry } from './types';

function taskPrefix(status: string, role: string): string {
  switch (status) {
    case 'done': return `✅ ${role} selesai`;
    case 'failed': return `⚠️ ${role} gagal`;
    case 'awaiting_clarification': return `❓ ${role} butuh keputusan`;
    case 'pending': return `🕓 ${role} dijadwalkan`;
    case 'in_progress': return `⏳ ${role} sedang bekerja`;
    case 'stopping': return `⏹️ ${role} sedang dihentikan`;
    case 'stopped': return `⏹️ ${role} dihentikan`;
    default: return role;
  }
}

export function renderTaskCardElement(entry: TranscriptEntry, options?: { restore?: boolean }): HTMLElement {
  const restore = options?.restore === true;
  const status = entry.task_status || '';
  const taskID = entry.task_id || '';
  const role = entry.task_role || 'task';
  const previousStatus = taskID ? taskStatuses.get(taskID) : undefined;
  const terminal = new Set(['done', 'failed', 'stopped']);
  if (previousStatus && terminal.has(previousStatus) && !terminal.has(status)) {
    const existing = taskID ? taskBubbles.get(taskID) : undefined;
    if (existing?.isConnected) return existing;
  }

  if (taskID) taskStatuses.set(taskID, status);
  if (!restore) {
    if (status === 'pending' || status === 'in_progress') setRingState('delegating');
    else if (status === 'awaiting_clarification') setRingState('needs-input');
    else {
      const active = [...taskStatuses.values()].some((v) => v === 'pending' || v === 'in_progress' || v === 'stopping');
      if (!active) setRingState('idle');
      if (terminal.has(status)) void refreshRuntimeStatus();
    }
  }

  const prefix = taskPrefix(status, role);
  const summary = (entry.summary || '').trim();
  const isTerminal = terminal.has(status) || status === 'awaiting_clarification';
  let activity = '';
  if (!isTerminal && summary && summary.length <= 120) activity = summary;
  else if ((status === 'failed' || status === 'awaiting_clarification') && summary.length <= 200) activity = summary;
  const text = activity ? `**${prefix}**\n\n${activity}` : `**${prefix}**`;

  let item = taskID ? taskBubbles.get(taskID) : undefined;
  if (!item || !item.isConnected) {
    item = document.createElement('div');
    item.className = 'message message--task transcript-entry transcript-task';
    item.dataset.entryKind = 'task';
    if (taskID) {
      item.dataset.taskId = taskID;
      item.dataset.taskRole = role;
      taskBubbles.set(taskID, item);
      item.classList.add('message--task-clickable');
      item.setAttribute('role', 'button');
      item.setAttribute('tabindex', '0');
      const open = () => {
        void import('../task-monitor-overlay').then((m) => {
          void m.openTaskMonitor({ taskID, role: item!.dataset.taskRole });
        });
      };
      item.addEventListener('click', open);
      item.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); open(); }
      });
    }
  }
  const body = document.createElement('div');
  body.className = 'transcript-task-body';
  body.append(renderMarkdown(text));
  item.replaceChildren(body);
  if (taskID && (status === 'failed' || status === 'stopped')) {
    const resume = document.createElement('button');
    resume.type = 'button';
    resume.className = 'task-card-resume';
    resume.textContent = 'Lanjutkan task';
    resume.addEventListener('click', (event) => {
      event.stopPropagation();
      resume.disabled = true;
      void ResumeTask(getSessionID(), taskID)
        .then(() => {
          setRingState('delegating');
          void refreshRuntimeStatus();
          void restoreChatHistory();
        })
        .catch(() => {
          resume.disabled = false;
        });
    });
    item.append(resume);
  }
  return item;
}

export function patchTaskCardElement(el: HTMLElement, entry: TranscriptEntry, options?: { restore?: boolean }) {
  const next = renderTaskCardElement(entry, options);
  if (next !== el) {
    el.replaceWith(next);
  }
}
