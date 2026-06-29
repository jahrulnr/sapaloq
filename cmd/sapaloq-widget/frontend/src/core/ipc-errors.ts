/** True for widget↔core unix socket transport failures (not model/provider errors). */
export function isSocketIPCError(message: string): boolean {
  const m = message.toLowerCase();
  return m.includes('sapaloq.sock') ||
    m.includes('i/o timeout') ||
    m.includes('connection refused') ||
    m.includes('broken pipe') ||
    m.includes('connection reset');
}

export function friendlyIPCError(err: unknown): string {
  const raw = err instanceof Error ? err.message : typeof err === 'string' ? err : 'unknown error';
  if (raw.includes('dial ')) return 'core offline';
  if (isSocketIPCError(raw)) return 'core tidak merespons — coba lagi sebentar';
  return raw;
}
