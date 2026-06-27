import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// Mock the Wails bindings the overlay polls. RuntimeStatus resolves the actor
// ids/liveness; TaskInspect returns the projected progress stream + plan.
const runtimeStatusMock = vi.fn();
const taskInspectMock = vi.fn();
vi.mock('../../wailsjs/go/main/App', () => ({
  RuntimeStatus: (...args: unknown[]) => runtimeStatusMock(...args),
  TaskInspect: (...args: unknown[]) => taskInspectMock(...args),
}));

import { openTaskMonitor, closeTaskMonitor } from './task-monitor-overlay';

function makeInspect(overrides: Partial<{ id: string; role: string; status: string; task: string; plan: string; events: Array<Record<string, unknown>>; event_count: number }>) {
  return {
    id: 'task-1',
    role: 'planner',
    status: 'in_progress',
    task: 'plan the work',
    events: [],
    event_count: 0,
    updated_at: '2026-06-27T07:00:00Z',
    ...overrides,
  } as any;
}

function makeActor(role: string, status: string, id = 'task-1') {
  return { id, role, status, phase: status === 'in_progress' ? 'working' : 'idle', workspace: '/tmp' };
}

describe('task-monitor-overlay', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    document.body.innerHTML = '';
    runtimeStatusMock.mockReset();
    taskInspectMock.mockReset();
    runtimeStatusMock.mockResolvedValue({ actors: [makeActor('planner', 'in_progress', 'task-1')] });
    taskInspectMock.mockResolvedValue(makeInspect({}));
  });

  afterEach(() => {
    closeTaskMonitor();
    vi.useRealTimers();
  });

  it('renders the modal with Planner tab active by default', async () => {
    const p = openTaskMonitor({ tab: 'planner' });
    await vi.advanceTimersByTimeAsync(0);
    await p;
    const overlay = document.getElementById('task-monitor-overlay');
    expect(overlay).not.toBeNull();
    const plannerTab = overlay?.querySelector('.task-monitor-tab[data-tab="planner"]') as HTMLButtonElement;
    expect(plannerTab.classList.contains('is-active')).toBe(true);
  });

  it('shows the planner plan sub-tab and renders plan markdown', async () => {
    taskInspectMock.mockResolvedValue(makeInspect({ plan: '# Plan\n- step one' }));
    await vi.advanceTimersByTimeAsync(0);
    await openTaskMonitor({ tab: 'planner' });
    await vi.advanceTimersByTimeAsync(0);
    const overlay = document.getElementById('task-monitor-overlay')!;
    expect(overlay.querySelector('.task-monitor-subtab[data-sub="plan"]')).not.toBeNull();
    // Switch to the Plan sub-tab.
    (overlay.querySelector('.task-monitor-subtab[data-sub="plan"]') as HTMLButtonElement).click();
    const planPane = overlay.querySelector('.task-monitor-plan');
    expect(planPane).not.toBeNull();
    expect(planPane?.textContent).toContain('step one');
  });

  it('coalesces consecutive response deltas into a single assistant text entry', async () => {
    taskInspectMock.mockResolvedValue(makeInspect({
      events: [
        { kind: 'thinking_delta', delta: 'hmm' },
        { kind: 'response_delta', delta: 'Hello ' },
        { kind: 'response_delta', delta: 'world' },
        { kind: 'tool_call', tool_name: 'read_file', tool_arguments: '{"path":"/tmp/x"}' },
      ],
      event_count: 4,
    }));
    await vi.advanceTimersByTimeAsync(0);
    await openTaskMonitor({ tab: 'planner' });
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(0);
    const overlay = document.getElementById('task-monitor-overlay')!;
    const texts = overlay.querySelectorAll('.task-monitor-text');
    expect(texts.length).toBe(1);
    expect(texts[0].textContent).toContain('Hello world');
    const tools = overlay.querySelectorAll('.task-monitor-tool');
    expect(tools.length).toBe(1);
    expect(tools[0].textContent).toContain('read_file');
    const thinking = overlay.querySelectorAll('.task-monitor-thinking');
    expect(thinking.length).toBe(1);
  });

  it('emits a turn boundary divider between turns', async () => {
    taskInspectMock.mockResolvedValue(makeInspect({
      events: [
        { kind: 'response_delta', delta: 'turn 1' },
        { kind: 'turn_boundary' },
        { kind: 'response_delta', delta: 'turn 2' },
      ],
      event_count: 3,
    }));
    await vi.advanceTimersByTimeAsync(0);
    await openTaskMonitor({ tab: 'planner' });
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(0);
    const overlay = document.getElementById('task-monitor-overlay')!;
    const turns = overlay.querySelectorAll('.task-monitor-turn');
    expect(turns.length).toBe(1);
  });

  it('close button removes the overlay from the DOM', async () => {
    await openTaskMonitor({ tab: 'planner' });
    await vi.advanceTimersByTimeAsync(0);
    expect(document.getElementById('task-monitor-overlay')).not.toBeNull();
    (document.querySelector('.task-monitor-close') as HTMLButtonElement).click();
    expect(document.getElementById('task-monitor-overlay')).toBeNull();
  });

  it('Escape dismisses the overlay', async () => {
    await openTaskMonitor({ tab: 'planner' });
    await vi.advanceTimersByTimeAsync(0);
    expect(document.getElementById('task-monitor-overlay')).not.toBeNull();
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    expect(document.getElementById('task-monitor-overlay')).toBeNull();
  });

  it('backdrop click dismisses but panel click does not', async () => {
    await openTaskMonitor({ tab: 'planner' });
    await vi.advanceTimersByTimeAsync(0);
    const overlay = document.getElementById('task-monitor-overlay')!;
    // Click inside the panel -> stays open.
    (overlay.querySelector('.task-monitor-panel') as HTMLElement).click();
    expect(document.getElementById('task-monitor-overlay')).not.toBeNull();
    // Click the backdrop (overlay root) -> closes.
    overlay.click();
    expect(document.getElementById('task-monitor-overlay')).toBeNull();
  });

  it('switches to the Agent tab and fetches that task', async () => {
    runtimeStatusMock.mockResolvedValue({
      actors: [makeActor('planner', 'in_progress', 'task-p'), makeActor('task-runner', 'in_progress', 'task-a')],
    });
    taskInspectMock.mockImplementation(async (taskID: string) => makeInspect({
      id: taskID, role: 'task-runner', task: 'agent work', events: [{ kind: 'response_delta', delta: 'working' }],
      event_count: 1,
    }));
    await openTaskMonitor({ tab: 'agent' });
    await vi.advanceTimersByTimeAsync(0);
    const overlay = document.getElementById('task-monitor-overlay')!;
    const agentTab = overlay.querySelector('.task-monitor-tab[data-tab="agent"]') as HTMLButtonElement;
    expect(agentTab.classList.contains('is-active')).toBe(true);
    expect(taskInspectMock).toHaveBeenCalledWith('task-a', expect.any(Number));
    // Agent executing a handed-off plan exposes a Plan sub-tab only when a plan
    // is present; with no plan here there should be no plan sub-tab.
    expect(overlay.querySelector('.task-monitor-subtab[data-sub="plan"]')).toBeNull();
  });

  it('shows an idle empty state when the actor has no task id', async () => {
    runtimeStatusMock.mockResolvedValue({ actors: [] });
    await openTaskMonitor({ tab: 'planner' });
    await vi.advanceTimersByTimeAsync(0);
    const overlay = document.getElementById('task-monitor-overlay')!;
    expect(overlay.querySelector('.task-monitor-empty')?.textContent).toContain('tidak aktif');
  });
});
