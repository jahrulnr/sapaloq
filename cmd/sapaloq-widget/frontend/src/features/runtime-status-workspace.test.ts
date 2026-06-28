import { describe, it, expect, beforeEach } from 'vitest';
import { applyWorkspacePath, renderRuntimeStatus } from './runtime-status';
import type { RuntimeStatus } from '../core/types';
import { setSessionID } from '../core/state';

describe('runtime-status workspace label', () => {
  beforeEach(() => {
    setSessionID('');
    document.body.innerHTML = `
      <button id="runtime-workspace" title="default">
        <span class="runtime-kicker">WORKSPACE</span>
        <strong>~/SapaLOQ/workspace</strong>
      </button>
    `;
  });

  it('applyWorkspacePath updates the card for the active session', () => {
    setSessionID('sess-a');
    applyWorkspacePath('/home/me/proj', 'sess-a');
    const el = document.getElementById('runtime-workspace')!;
    expect(el.dataset.workspacePath).toBe('/home/me/proj');
    expect(el.querySelector('strong')?.textContent).toBe('~/proj');
  });

  it('does not paint another room workspace onto the active card', () => {
    setSessionID('sess-b');
    applyWorkspacePath('/home/me/other', 'sess-a');
    const el = document.getElementById('runtime-workspace')!;
    expect(el.dataset.workspacePath).not.toBe('/home/me/other');
    expect(el.querySelector('strong')?.textContent).toBe('~/SapaLOQ/workspace');
  });

  it('renderRuntimeStatus uses per-session cache only for the active room', () => {
    setSessionID('sess-a');
    applyWorkspacePath('/home/me/proj', 'sess-a');
    setSessionID('sess-b');
    renderRuntimeStatus({
      provider: 'p',
      model: 'm',
      driver: 'd',
      session_id: 'sess-b',
      config_path: '',
      data_path: '',
      memory_path: '',
      state_path: '',
      workspace_path: '/home/me/SapaLOQ/workspace',
      actors: [],
    } as RuntimeStatus);
    expect(document.getElementById('runtime-workspace')?.querySelector('strong')?.textContent)
      .toBe('~/SapaLOQ/workspace');
  });

  it('renderRuntimeStatus keeps cached workspace for the same session when core omits it', () => {
    setSessionID('sess-a');
    applyWorkspacePath('/home/me/proj', 'sess-a');
    renderRuntimeStatus({
      provider: 'p',
      model: 'm',
      driver: 'd',
      session_id: 'sess-a',
      config_path: '',
      data_path: '',
      memory_path: '',
      state_path: '',
      workspace_path: '/home/me/SapaLOQ/workspace',
      actors: [],
    } as RuntimeStatus);
    expect(document.getElementById('runtime-workspace')?.querySelector('strong')?.textContent).toBe('~/proj');
  });
});
