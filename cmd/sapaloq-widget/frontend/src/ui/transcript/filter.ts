import type { TranscriptEntry } from './types';

/** Internal loop-steer status from the orchestrator — not a human message. */
export function isAutopilotSteerEntry(entry: TranscriptEntry): boolean {
  const raw = (entry.text || entry.label || '').trim().toLowerCase();
  return raw.startsWith('continuing') && raw.includes('sapaloq_stop');
}

export function visibleTranscriptEntries(entries: TranscriptEntry[]): TranscriptEntry[] {
  return entries.filter(
    (e) => !isAutopilotSteerEntry(e) && (e.kind !== 'text' || (e.text || '').trim()),
  );
}
