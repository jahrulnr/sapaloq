// Apply a BE-issued session reset — FE clears only when reset=true.
import type { TranscriptEntryInput } from '../ui/transcript';
import { setSessionID } from '../core/state';
import { clearMessages } from './messages';
import { mountChatTranscript, resetChatTranscriptState } from './transcript-pane';

export type SessionResetPayload = {
  reset?: boolean;
  session_id?: string;
  entries?: ReadonlyArray<TranscriptEntryInput>;
};

/** Returns true when the patch carried reset and the UI was updated. */
export function applyChatResetFromBE(patch: SessionResetPayload): boolean {
  if (!patch.reset) return false;
  if (patch.session_id) setSessionID(patch.session_id);
  resetChatTranscriptState();
  clearMessages();
  mountChatTranscript(patch.entries || []);
  return true;
}
