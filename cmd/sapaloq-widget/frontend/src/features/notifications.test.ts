import { describe, it, expect, vi, beforeEach } from 'vitest';

// Mocks are defined at the top level so each dynamically-imported module
// instance picks them up. Per-test state is reset in beforeEach.
const notificationSoundMock = vi.fn();
const notificationSoundForRoleMock = vi.fn();
const isAvailableMock = vi.fn();
const initializeMock = vi.fn();
const requestAuthMock = vi.fn();
const sendNotificationMock = vi.fn();

vi.mock('../../wailsjs/go/main/App', () => ({
  NotificationSound: (...args: unknown[]) => notificationSoundMock(...args),
  NotificationSoundForRole: (...args: unknown[]) => notificationSoundForRoleMock(...args),
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({
  IsNotificationAvailable: (...a: unknown[]) => isAvailableMock(...a),
  InitializeNotifications: (...a: unknown[]) => initializeMock(...a),
  RequestNotificationAuthorization: (...a: unknown[]) => requestAuthMock(...a),
  SendNotification: (...a: unknown[]) => sendNotificationMock(...a),
}));

// The notifications module caches singleton state (audio buffer + init flag),
// so each test re-imports a fresh instance via resetModules.
async function loadFresh() {
  vi.resetModules();
  const mod = await import('./notifications');
  return mod;
}

describe('notifications', () => {
  beforeEach(() => {
    notificationSoundMock.mockReset();
    notificationSoundForRoleMock.mockReset();
    isAvailableMock.mockReset();
    initializeMock.mockReset();
    requestAuthMock.mockReset();
    sendNotificationMock.mockReset();
    notificationSoundMock.mockResolvedValue('data:audio/wav;base64,ORCH');
    notificationSoundForRoleMock.mockImplementation(async (role: string) => `data:audio/wav;base64,${String(role).toUpperCase()}`);
    isAvailableMock.mockResolvedValue(true);
    requestAuthMock.mockResolvedValue(true);
    initializeMock.mockResolvedValue(undefined);
    sendNotificationMock.mockResolvedValue(undefined);
    // jsdom logs "Not implemented" warnings for HTMLMediaElement.play/pause;
    // stub them so the swallowed-chime path stays quiet.
    const proto = HTMLAudioElement.prototype as unknown as { play: () => Promise<void>; pause: () => void };
    proto.play = () => Promise.resolve();
    proto.pause = () => {};
  });

  it('plays the chime and posts a native toast for an orchestrator finish', async () => {
    const { notifyCompletion } = await loadFresh();
    await notifyCompletion('orchestrator', 'SapaLOQ selesai', 'Run selesai.');
    // Orchestrator run uses the generic chime (NotificationSound), not a role.
    expect(notificationSoundMock).toHaveBeenCalled();
    expect(notificationSoundForRoleMock).not.toHaveBeenCalled();
    expect(initializeMock).toHaveBeenCalled();
    expect(sendNotificationMock).toHaveBeenCalledTimes(1);
    const opts = sendNotificationMock.mock.calls[0][0];
    expect(opts.title).toBe('SapaLOQ selesai');
    expect(opts.body).toBe('Run selesai.');
    expect(opts.id).toContain('orchestrator:');
  });

  it('uses a task-namespaced toast id for sub-agent completions', async () => {
    const { notifyCompletion } = await loadFresh();
    await notifyCompletion('task', 'Planner selesai', '', 'planner');
    const opts = sendNotificationMock.mock.calls[0][0];
    expect(opts.id).toContain('task:');
    expect(opts.title).toBe('Planner selesai');
  });

  it('fetches the role-specific chime for planner/agent completions', async () => {
    const { notifyCompletion } = await loadFresh();
    await notifyCompletion('task', 'Planner selesai', '', 'planner');
    await notifyCompletion('task', 'Agent selesai', '', 'task-runner');
    expect(notificationSoundForRoleMock).toHaveBeenCalledWith('planner');
    // 'agent' is normalized to 'task-runner' on the Go side.
    expect(notificationSoundForRoleMock).toHaveBeenCalledWith('task-runner');
    // The generic orchestrator chime must NOT be fetched for role completions.
    expect(notificationSoundMock).not.toHaveBeenCalled();
  });

  it('falls back to the generic chime for an unknown task role', async () => {
    const { notifyCompletion } = await loadFresh();
    await notifyCompletion('task', 'Task selesai', '', 'scribe');
    expect(notificationSoundMock).toHaveBeenCalled();
    expect(notificationSoundForRoleMock).not.toHaveBeenCalled();
  });

  it('skips the toast when notifications are unavailable but still fetches the chime', async () => {
    isAvailableMock.mockResolvedValue(false);
    const { notifyCompletion } = await loadFresh();
    await notifyCompletion('orchestrator', 'SapaLOQ selesai', 'x');
    expect(sendNotificationMock).not.toHaveBeenCalled();
    expect(notificationSoundMock).toHaveBeenCalled();
  });

  it('increments the toast id across calls so platforms do not collapse them', async () => {
    const { notifyCompletion } = await loadFresh();
    await notifyCompletion('task', 'a', '', 'planner');
    await notifyCompletion('task', 'b', '', 'planner');
    const id1 = sendNotificationMock.mock.calls[0][0].id;
    const id2 = sendNotificationMock.mock.calls[1][0].id;
    expect(id1).not.toEqual(id2);
  });

  it('primeNotifications eagerly loads the generic + role chimes', async () => {
    const { primeNotifications } = await loadFresh();
    primeNotifications();
    // Flush the lazy loads.
    await Promise.resolve();
    await Promise.resolve();
    expect(notificationSoundMock).toHaveBeenCalled();
    expect(notificationSoundForRoleMock).toHaveBeenCalledWith('planner');
    expect(notificationSoundForRoleMock).toHaveBeenCalledWith('task-runner');
  });
});
