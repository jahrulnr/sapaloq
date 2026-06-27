// Chat turn controller: send / retry / stop / delete + live transcript patches.
import { DeleteChatTurn, RetryChatTurn, SendMessage, StopChat } from '../../wailsjs/go/main/App';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import type { AttachmentData } from '../ui/compose';
import type { ChatUsage, TranscriptPatch } from '../core/types';
import { errorText, getComposeInput, getMessageList } from '../ui/dom';
import { ICON_SEND, ICON_STOP } from '../ui/icons';
import { autosizeCompose, resetComposeSize, setComposeDisabled } from '../ui/compose-ui';
import { renderUsage, setConnection, setRingState, runPing } from './connection';
import { appendMessage, closeMessageMenu } from './messages';
import { registerMessageActions } from './message-actions';
import { applyChatResetFromBE } from './apply-session-reset';
import { mountChatTranscript, resetChatTranscriptState, syncChatTranscript, syncChatTranscriptStateFromDOM } from './transcript-pane';
import { bindLatestGroupTurnID, loadSessionList, removeRepliesAfterTurn, restoreChatHistory } from './history';
import { hideSlashSuggest, refreshSlashSuggest } from './slash';
import { refreshRuntimeStatus } from './runtime-status';
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
} from '../core/state';

let activeGeneration: string | null = null;

function setSubmittingUI(active: boolean) {
  const button = document.getElementById('send-btn') as HTMLButtonElement | null;
  if (!button) return;
  button.dataset.mode = active ? 'stop' : 'send';
  button.setAttribute('aria-label', active ? 'Stop response' : 'Kirim');
  button.title = active ? 'Stop response' : 'Kirim';
  button.innerHTML = active ? ICON_STOP : ICON_SEND;
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
  if (patch.reset) {
    applyChatResetFromBE(patch);
    if (patch.finished) releaseInFlightTurn();
    if (patch.usage) renderUsage(patch.usage as ChatUsage);
    void loadSessionList();
    return;
  }
  if (!patch.entries?.length) return;
  if (activeGeneration && patch.generation_id && patch.generation_id !== activeGeneration) return;
  syncChatTranscript(patch.entries);
  if (patch.usage) renderUsage(patch.usage as ChatUsage);
  if (patch.finished) releaseInFlightTurn();
}

async function sendText(text: string, _visibleText = text, _attachments: AttachmentData[] = []) {
  const input = getComposeInput();
  if (isSubmitting() || !input || !text.trim()) return;
  closeMessageMenu();
  hideSlashSuggest();
  setLastSubmittedText(text);
  nextUserGroup();
  setRingState('thinking');
  setSubmitting(true);
  setSubmittingUI(true);
  setComposeDisabled(true);
  activeGeneration = null;
  syncChatTranscriptStateFromDOM();
  try {
    const res = await SendMessage(getSessionID(), text);
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
    const message = errorText(err);
    appendMessage('message--error', message.includes('dial ') ? 'core offline' : message, getUserGroup());
    setConnection(message.includes('dial ') ? 'disconnected' : 'connected');
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

async function retryMessage(turnID: number) {
  const input = getComposeInput();
  if (!turnID || isSubmitting() || !input) return;
  closeMessageMenu();
  setUserGroup(removeRepliesAfterTurn(turnID));
  setRingState('thinking');
  setSubmitting(true);
  setSubmittingUI(true);
  setComposeDisabled(true);
  activeGeneration = null;
  resetChatTranscriptState();
  try {
    const res = await RetryChatTurn(getSessionID(), turnID);
    setSessionID(res.session_id || getSessionID());
    if (res.generation_id) activeGeneration = res.generation_id;
    if (res.transcript?.length) mountChatTranscript(res.transcript);
    await bindLatestGroupTurnID();
    renderUsage(res.usage as ChatUsage | undefined);
  } catch (err) {
    appendMessage('message--error', errorText(err), getUserGroup(), turnID);
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
    appendMessage('message--error', errorText(err), getUserGroup(), turnID);
  }
}

export async function stopActiveResponse() {
  if (!isSubmitting()) return;
  try {
    await StopChat(getSessionID());
  } catch (err) {
    appendMessage('message--error', errorText(err), getUserGroup());
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
      if (isSubmitting()) {
        applyTranscriptPatch(patch);
        if (patch.finished) {
          void notifyCompletion('orchestrator', patch.entries?.some((e) => e.kind === 'error') ? 'SapaLOQ gagal' : 'SapaLOQ selesai',
            patch.finished ? 'Run selesai.' : '');
        }
        return;
      }
      // Background task cards: refresh full transcript when idle.
      if (patch.entries?.some((e) => e.kind === 'task')) {
        void restoreChatHistory();
        void refreshRuntimeStatus();
      }
    });
    EventsOn('sapaloq:stream', (event: { kind?: string; task_status?: string; task_role?: string; summary?: string }) => {
      if (event.kind === 'task_update') {
        void restoreChatHistory();
        void refreshRuntimeStatus();
        maybeNotifyTaskCompletion(event);
      }
    });
  } catch {
    // Wails runtime only
  }
  primeNotifications();
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
