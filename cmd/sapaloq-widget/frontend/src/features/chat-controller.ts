// Chat turn controller: owns the in-flight turn lifecycle (send / retry / stop /
// delete), the live stream renderer fed by the `sapaloq:stream` Wails event, and
// the small send/edit helpers the message bubbles dispatch into.
import { DeleteChatTurn, RetryChatTurn, SendMessage, StopChat } from '../../wailsjs/go/main/App';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import type { AttachmentData } from '../ui/compose';
import type { ChatUsage, StreamEvent } from '../core/types';
import { errorText, getComposeInput } from '../ui/dom';
import { ICON_SEND, ICON_STOP } from '../ui/icons';
import { autosizeCompose, resetComposeSize, setComposeDisabled } from '../ui/compose-ui';
import { renderUsage, setConnection, setRingState, runPing } from './connection';
import { appendMessage, closeMessageMenu } from './messages';
import { registerMessageActions } from './message-actions';
import {
  appendProgressBubble,
  clearProgressBubble,
  feedStreamEvent,
  finishStreamRenderer,
  newStreamRenderer,
  renderEvents,
  renderTaskUpdate,
} from './stream';
import { bindLatestGroupTurnID, removeRepliesAfterTurn, restoreChatHistory } from './history';
import { hideSlashSuggest, refreshSlashSuggest } from './slash';
import { refreshRuntimeStatus } from './runtime-status';
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
import type { StreamRenderer } from '../core/types';

// ---------------------------------------------------------------------------
// Live stream renderer for the in-flight turn
// ---------------------------------------------------------------------------

// When non-null, the batch result returned by SendMessage/RetryChatTurn is
// ignored (already rendered live); it's used only as a fallback otherwise.
let liveRenderer: StreamRenderer | null = null;
let liveEventsSeen = false;

function feedLiveEvent(event: StreamEvent) {
  if (!liveRenderer) liveRenderer = newStreamRenderer();
  liveEventsSeen = true;
  feedStreamEvent(liveRenderer, event);
}

// beginLiveStream arms the live renderer for a new turn. Call before invoking
// SendMessage/RetryChatTurn.
function beginLiveStream() {
  liveRenderer = newStreamRenderer();
  liveEventsSeen = false;
}

// finalizeLiveStream wraps up after the bound call resolves: if live events
// were delivered (Wails runtime), just flush any open bubbles; otherwise replay
// the batch result as a fallback (plain browser / no live transport).
function finalizeLiveStream(res: { events?: StreamEvent[] } | null | undefined) {
  if (liveEventsSeen) {
    if (liveRenderer) finishStreamRenderer(liveRenderer);
  } else {
    renderEvents((res?.events || []) as StreamEvent[]);
  }
  liveRenderer = null;
  liveEventsSeen = false;
}

// endLiveStream tears down the renderer on error/abort.
function endLiveStream() {
  if (liveRenderer) finishStreamRenderer(liveRenderer);
  liveRenderer = null;
  liveEventsSeen = false;
}

// ---------------------------------------------------------------------------
// Submit / send
// ---------------------------------------------------------------------------

function setSubmittingUI(active: boolean) {
  const button = document.getElementById('send-btn') as HTMLButtonElement | null;
  if (!button) return;
  button.dataset.mode = active ? 'stop' : 'send';
  button.setAttribute('aria-label', active ? 'Stop response' : 'Kirim');
  button.title = active ? 'Stop response' : 'Kirim';
  button.innerHTML = active ? ICON_STOP : ICON_SEND;
}

async function sendText(text: string, visibleText = text, attachments: AttachmentData[] = []) {
  const input = getComposeInput();
  if (isSubmitting() || !input || !text.trim()) return;
  closeMessageMenu();
  hideSlashSuggest();
  setLastSubmittedText(text);
  nextUserGroup();
  appendMessage('message--user', visibleText.trim(), getUserGroup(), 0, attachments);
  setRingState('thinking');
  appendProgressBubble('waiting');
  const thinkingTimer = window.setTimeout(() => appendProgressBubble('thinking'), 450);
  setSubmitting(true);
  setSubmittingUI(true);
  setComposeDisabled(true);
  beginLiveStream();
  try {
    const res = await SendMessage(getSessionID(), text);
    setSessionID(res.session_id || getSessionID());
    finalizeLiveStream(res);
    await bindLatestGroupTurnID();
    renderUsage(res.usage as ChatUsage | undefined);
    if (text.trim() === '/reset' || text.trim() === '/clear') {
      await restoreChatHistory();
    }
    void runPing();
  } catch (err) {
    endLiveStream();
    const message = errorText(err);
    appendMessage('message--error', message.includes('dial ') ? 'core offline' : message, getUserGroup());
    setConnection(message.includes('dial ') ? 'disconnected' : 'connected');
    setRingState('idle');
  } finally {
    window.clearTimeout(thinkingTimer);
    clearProgressBubble();
    setSubmitting(false);
    setSubmittingUI(false);
    setComposeDisabled(false);
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

// ---------------------------------------------------------------------------
// Retry / delete / stop / edit
// ---------------------------------------------------------------------------

async function retryMessage(turnID: number) {
  const input = getComposeInput();
  if (!turnID || isSubmitting() || !input) return;
  closeMessageMenu();
  setUserGroup(removeRepliesAfterTurn(turnID));
  setRingState('thinking');
  appendProgressBubble('waiting');
  const thinkingTimer = window.setTimeout(() => appendProgressBubble('thinking'), 450);
  setSubmitting(true);
  setSubmittingUI(true);
  setComposeDisabled(true);
  beginLiveStream();
  try {
    const res = await RetryChatTurn(getSessionID(), turnID);
    setSessionID(res.session_id || getSessionID());
    finalizeLiveStream(res);
    await bindLatestGroupTurnID();
    renderUsage(res.usage as ChatUsage | undefined);
  } catch (err) {
    endLiveStream();
    appendMessage('message--error', errorText(err), getUserGroup(), turnID);
  } finally {
    window.clearTimeout(thinkingTimer);
    clearProgressBubble();
    setSubmitting(false);
    setSubmittingUI(false);
    setComposeDisabled(false);
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
  appendProgressBubble('stopping');
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

// ---------------------------------------------------------------------------
// Wiring
// ---------------------------------------------------------------------------

// initChatController registers the turn-level message actions and subscribes to
// the live `sapaloq:stream` event. Call once at bootstrap.
export function initChatController() {
  registerMessageActions({
    retry: (turnID) => void retryMessage(turnID),
    delete: (turnID) => void deleteMessageBranch(turnID),
    edit: editText,
  });

  try {
    // Live stream: every chat event (thinking/response/tool/status/done) arrives
    // here as it is produced by the core, so deltas render incrementally instead
    // of bursting when SendMessage/RetryChatTurn resolves.
    EventsOn('sapaloq:stream', (event: StreamEvent) => {
      // Background task completions arrive asynchronously (no active chat
      // request), so they must be handled regardless of `submitting` - otherwise
      // the completion trigger would be silently dropped while idle.
      if (event.kind === 'task_update') {
        renderTaskUpdate(event);
        void refreshRuntimeStatus();
        return;
      }
      // The orchestrator SPEAKS a sub-agent's terminal outcome as a SINGLE,
      // whole response_delta stamped with task_id. This is a self-contained
      // completion line, not a streaming fragment, so it must be rendered as its
      // own assistant bubble and must NEVER be fed into the live renderer -
      // otherwise two concurrent completions (or a completion racing the active
      // turn) interleave their characters into one shared bubble. We also dedupe
      // per task_id so a re-published terminal transition can't append twice.
      if (event.kind === 'response_delta' && event.task_id) {
        const id = event.task_id;
        if (spokenTaskIDs.has(id)) return;
        spokenTaskIDs.add(id);
        const text = (event.delta || '').trim();
        if (text) appendMessage('message--assistant', text);
        return;
      }
      // A response_delta with no task_id while idle is an orphan completion
      // (legacy path): still surface it rather than silently dropping it.
      if (event.kind === 'response_delta' && !isSubmitting()) {
        const text = (event.delta || '').trim();
        if (text) appendMessage('message--assistant', text);
        return;
      }
      if (isSubmitting()) feedLiveEvent(event);
    });
  } catch {
    // EventsOn only exists inside a Wails runtime; ignore in plain browser.
  }
}
