import { describe, expect, it } from 'vitest';
import { friendlyIPCError, isSocketIPCError } from './ipc-errors';

describe('isSocketIPCError', () => {
  it('detects unix socket read timeout', () => {
    const msg = 'read unix @->/home/user/SapaLOQ/run/sapaloq.sock: i/o timeout';
    expect(isSocketIPCError(msg)).toBe(true);
    expect(friendlyIPCError(new Error(msg))).toContain('tidak merespons');
  });

  it('leaves provider errors unchanged', () => {
    const msg = 'provider-bridge: upstream status 503';
    expect(isSocketIPCError(msg)).toBe(false);
    expect(friendlyIPCError(new Error(msg))).toBe(msg);
  });
});
