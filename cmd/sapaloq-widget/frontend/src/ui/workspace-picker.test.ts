import { describe, it, expect, vi, beforeEach } from 'vitest';

const pickMock = vi.fn();
const setWorkspaceMock = vi.fn();
const refreshMock = vi.fn();

vi.mock('../../wailsjs/go/main/App', () => ({
  PickWorkspaceFolder: (...args: unknown[]) => pickMock(...args),
  SetWorkspace: (...args: unknown[]) => setWorkspaceMock(...args),
  RuntimeStatus: vi.fn().mockResolvedValue({ session_id: 'sess-1' }),
}));

vi.mock('../core/state', () => ({
  getSessionID: () => 'sess-1',
  setSessionID: vi.fn(),
}));

vi.mock('../features/runtime-status', () => ({
  applyWorkspacePath: vi.fn(),
  refreshRuntimeStatus: () => refreshMock(),
}));

import { wireWorkspaceCard } from './workspace-picker';

describe('workspace-picker', () => {
  beforeEach(() => {
    pickMock.mockReset();
    setWorkspaceMock.mockReset();
    refreshMock.mockReset();
    setWorkspaceMock.mockResolvedValue({ ok: true, path: '/home/me/new', session_id: 'sess-1' });
  });

  it('opens native dialog with stored path and persists selection', async () => {
    const btn = document.createElement('button');
    btn.id = 'runtime-workspace';
    btn.dataset.workspacePath = '/home/me/proj';
    document.body.append(btn);

    wireWorkspaceCard(btn);
    pickMock.mockResolvedValue('/home/me/new');
    btn.click();
    await vi.waitFor(() => expect(setWorkspaceMock).toHaveBeenCalled());

    expect(pickMock).toHaveBeenCalledWith('/home/me/proj');
    expect(setWorkspaceMock).toHaveBeenCalledWith('sess-1', '/home/me/new');
    expect(refreshMock).toHaveBeenCalled();
  });

  it('does nothing when dialog is cancelled', async () => {
    const btn = document.createElement('button');
    document.body.append(btn);
    wireWorkspaceCard(btn);
    pickMock.mockResolvedValue('');
    btn.click();
    await Promise.resolve();

    expect(setWorkspaceMock).not.toHaveBeenCalled();
    expect(refreshMock).not.toHaveBeenCalled();
  });
});
