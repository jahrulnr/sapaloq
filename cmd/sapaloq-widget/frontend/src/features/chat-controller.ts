// Chat turn controller: send / retry / stop / delete + live transcript patches.
import { DeleteChatTurn, RetryChatTurn, SendMessage, SteerChat, StopChat } from '../../wailsjs/go/main/App';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import type { AttachmentData } from '../ui/compose';
import type { ChatUsage, StreamEvent, TranscriptPatch } from '../core/types';
import { friendlyIPCError, ipcConnectionStateForError, isTransientIPCError } from '../core/ipc-errors';
import { errorText, getComposeInput, getMessageList, captureMessageScroll } from '../ui/dom';
import { autosizeCompose, resetComposeSize, setComposeDisabled } from '../ui/compose-ui';
import { renderUsage, setConnection, setRingState, runPing } from './connection';
import { appendMessage, closeMessageMenu } from './messages';
import { registerMessageActions } from './message-actions';
import { applyChatResetFromBE } from './apply-session-reset';
import { syncChatTranscript, syncChatTranscriptStateFromDOM, scheduleSyncChatTranscript, flushScheduledChatTranscript, applyDeltaChatTranscript, mountChatTranscript, resetChatTranscriptState, patchForegroundTaskCards } from './transcript-pane';
import { bindLatestGroupTurnID, loadSessionList, removeRepliesAfterTurn, restoreChatHistory, scheduleRestoreChatHistory, cancelScheduledRestoreChatHistory } from './history';
import { hideSlashSuggest, refreshSlashSuggest } from './slash';
import { refreshRuntimeStatus, currentSessionWorkspacePath } from './runtime-status';
import { notifyCompletion, primeNotifications } from './notifications';
import {
  getCompose,
  getSessionID,
  getUserGroup,
  isSubmitting,
  nextUserGroup,
  setLastSubmittedText,
  setSessionID,
  setSubmitting,
  setUserGroup,
  spokenTaskIDs,
} from '../core/state';

let activeGeneration: string | null = null;
let steeringPending = false;
let steeringStatusTimer: ReturnType<typeof setTimeout> | null = null;

export function setSubmittingUI(active: boolean) {
  const send = document.getElementById('send-btn') as HTMLButtonElement | null;
  const steer = document.getElementById('steer-btn') as HTMLButtonElement | null;
  const stop = document.getElementById('stop-btn') as HTMLButtonElement | null;
  const attach = document.getElementById('attach-btn') as HTMLButtonElement | null;
  const input = getComposeInput();
  send?.toggleAttribute('hidden', active);
  steer?.toggleAttribute('hidden', !active);
  stop?.toggleAttribute('hidden', !active);
  if (attach) attach.disabled = active;
  input?.closest('.compose-wrap')?.classList.toggle('is-steering', active);
  input?.setAttribute('data-placeholder', active ? 'Steer SapaLOQ…' : 'Ask anything');
  setComposeDisabled(false);
  if (active) {
    showSteeringStatus('Steering diterapkan setelah tool batch selesai.');
  } else if (steeringStatusTimer) {
    clearTimeout(steeringStatusTimer);
    steeringStatusTimer = null;
    const hint = document.getElementById('steering-hint');
    if (hint) {
      hint.textContent = '';
      delete hint.dataset.state;
    }
  }
}

export function markSteeringApplied() {
  document.querySelectorAll('.message--steering.is-pending').forEach((el) => {
    el.classList.remove('is-pending');
    el.classList.add('is-applied');
  });
  showSteeringStatus('Steering diterapkan.');
}

export function markSteeringSkipped() {
  document.querySelectorAll('.message--steering.is-pending').forEach((node) => {
    const el = node as HTMLElement;
    el.classList.remove('is-pending');
    el.classList.add('is-failed');
    el.title = 'Run berakhir sebelum steering diterapkan.';
  });
  showSteeringStatus('Steering tidak diterapkan — run sudah berakhir.', true);
}

function showSteeringStatus(message: string, error = false) {
  const hint = document.getElementById('steering-hint');
  if (!hint) return;
  if (steeringStatusTimer) clearTimeout(steeringStatusTimer);
  hint.textContent = message;
  hint.dataset.state = error ? 'error' : 'normal';
  if (message !== 'Steering diterapkan setelah tool batch selesai.') {
    steeringStatusTimer = setTimeout(() => showSteeringStatus('Steering diterapkan setelah tool batch selesai.'), 2400);
  }
}

function releaseInFlightTurn() {
  if (!isSubmitting()) return;
  clearProgressFromTranscript();
  setSubmitting(false);
  setSubmittingUI(false);
  setComposeDisabled(false);
  setRingState('idle');
  getComposeInput()?.focus();
}

function clearProgressFromTranscript() {
  getMessageList()?.querySelectorAll('.transcript-progress').forEach((n) => n.remove());
}

function applyTranscriptPatch(patch: TranscriptPatch) {
  // sapaloq:boundary ipc→widget — TranscriptPatch only; never merge provider bytes here.
  if (patch.reset) {
    applyChatResetFromBE(patch);
    if (patch.finished) releaseInFlightTurn();
    if (patch.usage) renderUsage(patch.usage as ChatUsage);
    void loadSessionList();
    return;
  }
  if (patch.finished) {
    cancelScheduledRestoreChatHistory();
    flushScheduledChatTranscript();
    releaseInFlightTurn();
    if (patch.usage) renderUsage(patch.usage as ChatUsage);
    const list = getMessageList();
    if (patch.entries?.length && list) {
      resetChatTranscriptState();
      mountChatTranscript(patch.entries, captureMessageScroll(list));
    }
    return;
  }
  if (patch.mode === 'delta' && patch.ops?.length) {
    if (activeGeneration && patch.generation_id && patch.generation_id !== activeGeneration) return;
    applyDeltaChatTranscript(patch.ops);
    if (patch.usage) renderUsage(patch.usage as ChatUsage);
    return;
  }
  if (!patch.entries?.length) return;
  if (patch.entries.some((e) => (e.kind === 'status' || e.kind === 'progress') && e.label === 'steering applied')) {
    markSteeringApplied();
  }
  if (patch.entries.some((e) => (e.kind === 'status' || e.kind === 'progress') && e.label === 'steering skipped - run ended')) {
    markSteeringSkipped();
  }
  if (activeGeneration && patch.generation_id && patch.generation_id !== activeGeneration) return;
  scheduleSyncChatTranscript(patch.entries);
  if (patch.usage) renderUsage(patch.usage as ChatUsage);
}

function handleChatIPCError(err: unknown, opts?: { turnID?: number; allowWhenIdle?: boolean }): void {
  if (isTransientIPCError(err)) {
    setConnection(ipcConnectionStateForError(err));
    return;
  }
  if (!opts?.allowWhenIdle && !isSubmitting()) return;
  appendMessage('message--error', friendlyIPCError(err), getUserGroup(), opts?.turnID);
  setConnection('connected');
}

async function sendText(text: string, _visibleText = text, attachments: AttachmentData[] = []) {
  const input = getComposeInput();
  if (isSubmitting() || !input || !text.trim()) return;
  closeMessageMenu();
  hideSlashSuggest();
  setLastSubmittedText(text);
  nextUserGroup();
  setRingState('thinking');
  setSubmitting(true);
  setSubmittingUI(true);
  setComposeDisabled(false);
  activeGeneration = null;
  syncChatTranscriptStateFromDOM();
  const hostAttachments = attachments.map((a) => ({
    name: a.name,
    path: a.path || '',
    isDir: a.isDir || false,
  }));
  try {
    const res = await SendMessage(getSessionID(), text, hostAttachments, currentSessionWorkspacePath());
    if (res.generation_id) activeGeneration = res.generation_id;
    if (res.reset) {
      applyChatResetFromBE({
        session_id: res.session_id,
        entries: res.transcript,
        reset: true,
      });
      void loadSessionList();
    } else {
      if (res.session_id) setSessionID(res.session_id);
      if (res.transcript?.length) syncChatTranscript(res.transcript);
    }
    await bindLatestGroupTurnID();
    renderUsage(res.usage as ChatUsage | undefined);
    void runPing();
  } catch (err) {
    if (isTransientIPCError(err)) {
      setConnection(ipcConnectionStateForError(err));
    } else if (isSubmitting()) {
      appendMessage('message--error', friendlyIPCError(err), getUserGroup());
      setConnection('connected');
    }
    setRingState('idle');
  } finally {
    clearProgressFromTranscript();
    setSubmitting(false);
    setSubmittingUI(false);
    setComposeDisabled(false);
    activeGeneration = null;
    input.focus();
  }
}

export async function submitMessage() {
  const compose = getCompose();
  if (!compose || compose.isEmpty()) return;
  const { visibleText, modelText, attachments } = compose.serialize();
  if (!modelText) return;
  compose.clear();
  resetComposeSize();
  await sendText(modelText, visibleText || attachments.map((a) => a.name).join(', '), attachments);
}

export async function submitSteering() {
  const compose = getCompose();
  if (!isSubmitting() || !compose || compose.isEmpty() || steeringPending) return;
  const { visibleText, attachments } = compose.serialize();
  const message = visibleText.trim();
  if (!message) return;
  if (attachments.length) {
    showSteeringStatus('Steering v1 hanya mendukung teks.', true);
    return;
  }

  const bubble = appendMessage('message--steering is-pending', message, getUserGroup());
  const steerButton = document.getElementById('steer-btn') as HTMLButtonElement | null;
  steeringPending = true;
  if (steerButton) steerButton.disabled = true;
  try {
    await SteerChat(getSessionID(), message);
    compose.clear();
    resetComposeSize();
    showSteeringStatus('Steering diterapkan setelah tool batch selesai.');
  } catch (err) {
    bubble?.classList.remove('is-pending');
    bubble?.classList.add('is-failed');
    if (bubble) bubble.title = errorText(err);
    showSteeringStatus(errorText(err), true);
  } finally {
    steeringPending = false;
    if (steerButton) steerButton.disabled = false;
    getComposeInput()?.focus();
  }
}

async function retryMessage(turnID: number) {
  const input = getComposeInput();
  if (!turnID || isSubmitting() || !input) return;
  closeMessageMenu();
  setUserGroup(removeRepliesAfterTurn(turnID));
  syncChatTranscriptStateFromDOM();
  setRingState('thinking');
  setSubmitting(true);
  setSubmittingUI(true);
  setComposeDisabled(false);
  activeGeneration = null;
  try {
    const res = await RetryChatTurn(getSessionID(), turnID, currentSessionWorkspacePath());
    setSessionID(res.session_id || getSessionID());
    if (res.generation_id) activeGeneration = res.generation_id;
    if (res.transcript?.length) syncChatTranscript(res.transcript);
    await bindLatestGroupTurnID();
    renderUsage(res.usage as ChatUsage | undefined);
  } catch (err) {
    handleChatIPCError(err, { turnID, allowWhenIdle: true });
  } finally {
    clearProgressFromTranscript();
    setSubmitting(false);
    setSubmittingUI(false);
    setComposeDisabled(false);
    activeGeneration = null;
    input.focus();
    setRingState('idle');
  }
}

async function deleteMessageBranch(turnID: number) {
  if (!turnID || isSubmitting()) return;
  closeMessageMenu();
  try {
    await DeleteChatTurn(getSessionID(), turnID);
    await restoreChatHistory();
  } catch (err) {
    handleChatIPCError(err, { turnID, allowWhenIdle: true });
  }
}

export async function stopActiveResponse() {
  if (!isSubmitting()) return;
  try {
    await StopChat(getSessionID());
    flushScheduledChatTranscript();
  } catch (err) {
    if (isTransientIPCError(err)) {
      setConnection(ipcConnectionStateForError(err));
      releaseInFlightTurn();
      return;
    }
    appendMessage('message--error', friendlyIPCError(err), getUserGroup());
  }
}

function editText(text: string) {
  const compose = getCompose();
  if (!compose) return;
  compose.clear();
  compose.insertText(text);
  compose.focus();
  autosizeCompose();
  void refreshSlashSuggest();
}

export function initChatController() {
  registerMessageActions({
    retry: (turnID) => void retryMessage(turnID),
    delete: (turnID) => void deleteMessageBranch(turnID),
    edit: editText,
  });

  try {
    EventsOn('sapaloq:transcript', (patch: TranscriptPatch) => {
	  // Background actor patches are consumed by the actor monitor and must
	  // never be merged into the parent chat transcript.
	  if (patch.actor_id) return;
      if (patch.generation_id && !activeGeneration) activeGeneration = patch.generation_id;
      if (isSubmitting() || patch.finished) {
        applyTranscriptPatch(patch);
        if (patch.finished) {
          void notifyCompletion('orchestrator', patch.entries?.some((e) => e.kind === 'error') ? 'SapaLOQ gagal' : 'SapaLOQ selesai',
            patch.finished ? 'Run selesai.' : '');
        }
        return;
      }
      // Background task cards: patch in place; never full ChatHistory restore (drops tool rows).
      if (patch.entries?.some((e) => e.kind === 'task')) {
        patchForegroundTaskCards(patch.entries.filter((e) => e.kind === 'task'));
        void refreshRuntimeStatus();
      }
    });
    EventsOn('sapaloq:stream', (event: StreamEvent) => {
      if (event.kind === 'response_delta' && event.task_id) {
        applySpokenTaskCompletion(event);
        return;
      }
      if (event.kind === 'task_update') {
        if (event.task_id) {
          patchForegroundTaskCards([{
            kind: 'task',
            id: `task-${event.task_id}`,
            task_id: event.task_id,
            task_role: event.task_role,
            task_status: event.task_status,
            summary: event.summary,
          }]);
        }
        void refreshRuntimeStatus();
        maybeNotifyTaskCompletion(event);
      }
    });
  } catch {
    // Wails runtime only
  }
  primeNotifications();
}

/** Live follow-up when a background sub-agent finishes (tool-like completion). */
export function applySpokenTaskCompletion(event: StreamEvent): boolean {
  if (event.kind !== 'response_delta' || !event.task_id || !event.delta?.trim()) return false;
  if (spokenTaskIDs.has(event.task_id)) return false;
  spokenTaskIDs.add(event.task_id);
  scheduleRestoreChatHistory();
  return true;
}

function maybeNotifyTaskCompletion(event: { task_status?: string; task_role?: string; summary?: string }) {
  const status = event.task_status || '';
  if (status !== 'done' && status !== 'failed' && status !== 'stopped') return;
  const role = event.task_role || 'task';
  let title = `${roleLabel(role)} selesai`;
  if (status === 'failed') title = `${roleLabel(role)} gagal`;
  else if (status === 'stopped') title = `${roleLabel(role)} dihentikan`;
  void notifyCompletion('task', title, (event.summary || '').trim().slice(0, 200), role);
}

function roleLabel(role: string): string {
  if (role === 'task-runner') return 'Agent';
  if (role === 'planner') return 'Planner';
  if (role === 'scribe') return 'Scribe';
  return role || 'Task';
}
