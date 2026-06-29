import type { ConnectionState } from './types';

/** True for widget↔core unix socket transport failures (not model/provider errors). */
export function isSocketIPCError(message: string): boolean {
  const m = message.toLowerCase();
  return m.includes('sapaloq.sock') ||
    m.includes('i/o timeout') ||
    m.includes('connection refused') ||
    m.includes('broken pipe') ||
    m.includes('connection reset');
}

export function isDialIPCError(message: string): boolean {
  return message.includes('dial ');
}

export function ipcConnectionStateForError(err: unknown): ConnectionState {
  const raw = err instanceof Error ? err.message : typeof err === 'string' ? err : '';
  return isDialIPCError(raw) ? 'disconnected' : 'reconnecting';
}

/** Socket transport failures are transient; never paint them into the chat transcript. */
export function isTransientIPCError(err: unknown): boolean {
  const raw = err instanceof Error ? err.message : typeof err === 'string' ? err : '';
  return isDialIPCError(raw) || isSocketIPCError(raw);
}

export function friendlyIPCError(err: unknown): string {
  const raw = err instanceof Error ? err.message : typeof err === 'string' ? err : 'unknown error';
  if (isDialIPCError(raw)) return 'core offline';
  if (isSocketIPCError(raw)) return 'core tidak merespons — coba lagi sebentar';
  return raw;
}

export function friendlyIPCErrorText(text: string): string {
  return friendlyIPCError(text);
}
