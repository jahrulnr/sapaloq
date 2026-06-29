import { describe, expect, it } from 'vitest';
import {
  friendlyIPCError,
  friendlyIPCErrorText,
  ipcConnectionStateForError,
  isSocketIPCError,
  isTransientIPCError,
} from './ipc-errors';

describe('isSocketIPCError', () => {
  it('detects unix socket read timeout', () => {
    const msg = 'read unix @->/home/user/SapaLOQ/run/sapaloq.sock: i/o timeout';
    expect(isSocketIPCError(msg)).toBe(true);
    expect(isTransientIPCError(new Error(msg))).toBe(true);
    expect(friendlyIPCError(new Error(msg))).toContain('tidak merespons');
    expect(friendlyIPCErrorText(msg)).toContain('tidak merespons');
    expect(ipcConnectionStateForError(new Error(msg))).toBe('reconnecting');
  });

  it('detects dial offline', () => {
    const msg = 'dial /home/user/SapaLOQ/run/sapaloq.sock: connect: connection refused';
    expect(isTransientIPCError(new Error(msg))).toBe(true);
    expect(ipcConnectionStateForError(new Error(msg))).toBe('disconnected');
  });

  it('leaves provider errors unchanged', () => {
    const msg = 'provider-bridge: upstream status 503';
    expect(isSocketIPCError(msg)).toBe(false);
    expect(isTransientIPCError(new Error(msg))).toBe(false);
    expect(friendlyIPCError(new Error(msg))).toBe(msg);
  });
});
