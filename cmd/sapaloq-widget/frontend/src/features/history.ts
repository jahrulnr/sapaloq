// Chat-history restore + turn rendering, plus the helpers that bind/clear turn
// ids when retrying or deleting a branch.
import { ChatHistory } from '../../wailsjs/go/main/App';
import type { ChatTurn, ChatUsage } from '../core/types';
import { appendMessage, appendThinkingBubble, clearMessages, parseTurnContent } from './messages';
import { renderUsage } from './connection';
import {
  getUserGroup,
  nextUserGroup,
  setSessionID,
  getSessionID,
} from '../core/state';

function renderTurn(turn: ChatTurn) {
  if (!turn.content) return;
  // "tool" turns ([Tool results]…) are persisted only so they count toward
  // context usage - they are internal and must never surface as a chat bubble.
  if (turn.role === 'tool') return;
  if (turn.role === 'thinking') {
    appendThinkingBubble(turn.content);
    return;
  }
  const parsed = parseTurnContent(turn.content);
  if (turn.role === 'user') {
    nextUserGroup();
    appendMessage('message--user', parsed.text || parsed.attachments.map((item) => item.name).join(', '), getUserGroup(), turn.id, parsed.attachments);
  } else if (turn.role === 'error') appendMessage('message--error', turn.content, getUserGroup(), turn.id);
  else if (turn.role === 'assistant') appendMessage('message--assistant', turn.content, getUserGroup(), turn.id);
}

export async function restoreChatHistory() {
  try {
    const history = await ChatHistory();
    setSessionID(history.session_id || getSessionID());
    clearMessages();
    (history.turns || []).forEach((turn: ChatTurn) => renderTurn(turn));
    renderUsage(history.usage as ChatUsage | undefined);
  } catch {
    // Core may not be ready yet; ping loop will update connection state.
  }
}

export async function bindLatestGroupTurnID() {
  try {
    const history = await ChatHistory();
    const turns = (history.turns || []) as ChatTurn[];
    const user = [...turns].reverse().find((turn) => turn.role === 'user');
    if (!user) return 0;
    document.querySelectorAll<HTMLElement>(`.message[data-group="${getUserGroup()}"]`).forEach((item) => {
      item.dataset.turnId = `${user.id}`;
    });
    return user.id;
  } catch {
    return 0;
  }
}

// removeRepliesAfterTurn clears stale assistant/thinking/tool/error bubbles that
// belong to the retried user turn (and everything after it) so the regenerated
// response can stream into the same group instead of stacking on top of the old
// reply. Returns the group id of the retried user message so streamed events
// render in the correct place.
export function removeRepliesAfterTurn(turnID: number): number {
  const user = document.querySelector<HTMLElement>(`.message--user[data-turn-id="${turnID}"]`);
  if (!user) return getUserGroup();
  const group = Number(user.dataset.group || getUserGroup());
  document.querySelectorAll<HTMLElement>('#message-list > .message').forEach((item) => {
    const itemGroup = Number(item.dataset.group || 0);
    if (itemGroup < group) return;
    if (item === user) return;
    if (itemGroup === group && item.classList.contains('message--user')) return;
    item.remove();
  });
  return group;
}
