import type { TranscriptEntry } from './types';

/** Internal loop-steer status from the orchestrator — not a human message. */
export function isAutopilotSteerEntry(entry: TranscriptEntry): boolean {
  const raw = (entry.text || entry.label || '').trim().toLowerCase();
  return raw.startsWith('continuing') && raw.includes('sapaloq_stop');
}

export function visibleTranscriptEntries(
  entries: TranscriptEntry[],
  mode: 'chat' | 'monitor' = 'chat',
): TranscriptEntry[] {
  return entries.filter((e) => {
    if (isAutopilotSteerEntry(e)) return false;
    // Task cards belong in the orchestrator chat only, not the sub-agent monitor.
    if (mode === 'monitor' && e.kind === 'task') return false;
    return e.kind !== 'text' || (e.text || '').trim();
  });
}
