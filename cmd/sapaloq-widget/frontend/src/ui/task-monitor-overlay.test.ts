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

function makeInspect(overrides: Partial<{ id: string; role: string; status: string; task: string; plan: string; transcript: Array<Record<string, unknown>>; event_count: number }>) {
  return {
    id: 'task-1',
    role: 'planner',
    status: 'in_progress',
    task: 'plan the work',
    transcript: [],
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
      transcript: [
        { kind: 'thinking', text: 'hmm' },
        { kind: 'text', text: 'Hello world' },
        { kind: 'tool', tool_name: 'read_file', tool_args: '{"path":"/tmp/x"}' },
      ],
      event_count: 4,
    }));
    await vi.advanceTimersByTimeAsync(0);
    await openTaskMonitor({ tab: 'planner' });
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(0);
    const overlay = document.getElementById('task-monitor-overlay')!;
    const texts = overlay.querySelectorAll('.transcript-text');
    expect(texts.length).toBe(1);
    expect(texts[0].textContent).toContain('Hello world');
    const tools = overlay.querySelectorAll('.tool-activity');
    expect(tools.length).toBe(1);
    expect(tools[0].textContent).toContain('read_file');
    const thinking = overlay.querySelectorAll('.transcript-thinking');
    expect(thinking.length).toBe(1);
  });

  it('splits assistant text at turn boundaries without a divider row', async () => {
    taskInspectMock.mockResolvedValue(makeInspect({
      transcript: [
        { kind: 'text', text: 'turn 1' },
        { kind: 'text', text: 'turn 2' },
      ],
      event_count: 3,
    }));
    await vi.advanceTimersByTimeAsync(0);
    await openTaskMonitor({ tab: 'planner' });
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(0);
    const overlay = document.getElementById('task-monitor-overlay')!;
    expect(overlay.querySelectorAll('.task-monitor-turn')).toHaveLength(0);
    const texts = overlay.querySelectorAll('.transcript-text');
    expect(texts).toHaveLength(2);
    expect(texts[0].textContent).toContain('turn 1');
    expect(texts[1].textContent).toContain('turn 2');
  });

  it('drops ephemeral Menjalankan task hints from the activity stream', async () => {
    taskInspectMock.mockResolvedValue(makeInspect({
      transcript: [
        { kind: 'tool', tool_id: 'call-1', tool_name: 'exec', tool_args: '{"command":"ls"}' },
      ],
      event_count: 2,
    }));
    await openTaskMonitor({ tab: 'planner' });
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(0);
    const overlay = document.getElementById('task-monitor-overlay')!;
    expect(overlay.querySelector('.task-monitor-task-line')).toBeNull();
    expect(overlay.textContent).not.toContain('Menjalankan');
    expect(overlay.querySelectorAll('.tool-activity')).toHaveLength(1);
  });

  it('drops orchestrator task cards from the sub-agent activity stream', async () => {
    runtimeStatusMock.mockResolvedValue({
      actors: [makeActor('task-runner', 'in_progress', 'task-agent')],
    });
    taskInspectMock.mockResolvedValue(makeInspect({
      id: 'task-agent',
      role: 'task-runner',
      task: 'build site',
      transcript: [
        { kind: 'text', text: 'Building files.' },
        {
          kind: 'task',
          task_id: 'task-agent',
          task_role: 'task-runner',
          task_status: 'in_progress',
          summary: 'Menjalankan `exec`.',
        },
      ],
      event_count: 2,
    }));
    await openTaskMonitor({ tab: 'agent' });
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(0);
    const overlay = document.getElementById('task-monitor-overlay')!;
    expect(overlay.querySelector('.message--task')).toBeNull();
    expect(overlay.textContent).not.toContain('Menjalankan');
    expect(overlay.querySelectorAll('.transcript-text')).toHaveLength(1);
  });

  it('hides autopilot continuing nudges from the activity pane', async () => {
    taskInspectMock.mockResolvedValue(makeInspect({
      transcript: [
        { kind: 'text', text: 'Working on files.' },
        { kind: 'user', text: 'continuing - call `sapaloq_stop` to finish' },
        { kind: 'text', text: 'Next step.' },
      ],
      event_count: 3,
    }));
    await openTaskMonitor({ tab: 'planner' });
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(0);
    const overlay = document.getElementById('task-monitor-overlay')!;
    expect(overlay.querySelector('.transcript-user')).toBeNull();
    expect(overlay.textContent).not.toContain('continuing');
    expect(overlay.querySelectorAll('.transcript-text')).toHaveLength(2);
  });

  it('renders request and response inside one expandable tool activity block', async () => {
    const longArgs = '{"command":"' + 'cd /tmp/profile && '.repeat(20) + '"}';
    taskInspectMock.mockResolvedValue(makeInspect({
      transcript: [
        { kind: 'tool', tool_id: 'call-1', tool_name: 'exec', tool_args: longArgs, tool_result: 'installed successfully', tool_status: 'completed' },
      ],
      event_count: 2,
    }));
    await vi.advanceTimersByTimeAsync(0);
    await openTaskMonitor({ tab: 'planner' });
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(0);
    const overlay = document.getElementById('task-monitor-overlay')!;
    const tool = overlay.querySelector('.tool-activity') as HTMLElement | null;
    expect(tool).not.toBeNull();
    expect(tool!.classList.contains('is-open')).toBe(false);
    expect(tool!.querySelector<HTMLElement>('.tool-activity__body')?.hidden).toBe(true);
    expect(tool!.firstChild?.nodeType).toBe(3);
    expect(tool!.firstChild?.textContent).toContain('$ exec');
    expect(tool!.querySelector('button')).toBeNull();
    const sections = tool!.querySelectorAll('.tool-activity__section');
    expect(sections).toHaveLength(2);
    expect(sections[0].textContent).toContain('cd /tmp/profile');
    expect(sections[1].textContent).toContain('installed successfully');
    tool!.click();
    expect(tool!.classList.contains('is-open')).toBe(true);
    expect(tool!.querySelector<HTMLElement>('.tool-activity__body')?.hidden).toBe(false);
    tool!.click();
    expect(tool!.classList.contains('is-open')).toBe(false);
    expect(tool!.querySelector<HTMLElement>('.tool-activity__body')?.hidden).toBe(true);
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
      id: taskID, role: 'task-runner', task: 'agent work', transcript: [{ kind: 'text', text: 'working' }],
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

  it('pins a specific task when opened by task id (chat-bubble path)', async () => {
    // RuntimeStatus resolves the role's CURRENT agent to a different task than
    // the one we pin, proving the pin overrides role resolution so a non-latest
    // spawned agent is still viewable.
    runtimeStatusMock.mockResolvedValue({
      actors: [makeActor('task-runner', 'in_progress', 'task-latest')],
    });
    taskInspectMock.mockImplementation(async (taskID: string) => makeInspect({
      id: taskID, role: 'task-runner', task: 'older agent', status: 'done',
      transcript: [{ kind: 'text', text: 'older agent work' }], event_count: 1,
    }));
    await openTaskMonitor({ taskID: 'task-older', role: 'task-runner' });
    await vi.advanceTimersByTimeAsync(0);
    const overlay = document.getElementById('task-monitor-overlay')!;
    // The agent tab is active and inspected the PINNED task, not the latest.
    const agentTab = overlay.querySelector('.task-monitor-tab[data-tab="agent"]') as HTMLButtonElement;
    expect(agentTab.classList.contains('is-active')).toBe(true);
    expect(taskInspectMock).toHaveBeenCalledWith('task-older', expect.any(Number));
    expect(taskInspectMock).not.toHaveBeenCalledWith('task-latest', expect.any(Number));
  });

  it('shows an idle empty state when the actor has no task id', async () => {
    runtimeStatusMock.mockResolvedValue({ actors: [] });
    await openTaskMonitor({ tab: 'planner' });
    await vi.advanceTimersByTimeAsync(0);
    const overlay = document.getElementById('task-monitor-overlay')!;
    expect(overlay.querySelector('.task-monitor-empty')?.textContent).toContain('tidak aktif');
  });

  // Regression: a planner's task prompt can be a huge planning brief. It used
  // to render as an unbounded inline span that grew into a wall of text and
  // broke the header layout. It must now live in a collapsed, clamped
  // <details> whose summary is a single truncated line.
  it('renders a long task prompt as a collapsed, truncated details block', async () => {
    const longTask = 'dan /about, Drupal shim filters ' + 'x'.repeat(400);
    taskInspectMock.mockResolvedValue(makeInspect({ task: longTask, plan: '# Plan\n- step' }));
    await openTaskMonitor({ tab: 'planner' });
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(0);
    const overlay = document.getElementById('task-monitor-overlay')!;
    const details = overlay.querySelector('.task-monitor-task-details') as HTMLDetailsElement | null;
    expect(details).not.toBeNull();
    // Collapsed by default so the header stays compact.
    expect(details?.open).toBe(false);
    const summary = details?.querySelector('summary');
    expect(summary?.textContent?.endsWith('…')).toBe(true);
    // The full text is reachable inside the expandable body, not spilled into
    // the header line.
    const bodyNode = details?.querySelector('.task-monitor-task-body');
    expect(bodyNode?.textContent).toContain('xxxx');
    // The inline task span that used to blow up the layout is gone.
    expect(overlay.querySelector('.task-monitor-task')).toBeNull();
  });

  it('keeps accumulated activity across incremental polls with no new lines', async () => {
    taskInspectMock
      .mockResolvedValueOnce(makeInspect({
        transcript: [{ kind: 'text', text: 'building profile' }],
        event_count: 1,
      }))
      .mockResolvedValueOnce(makeInspect({
        transcript: [{ kind: 'text', text: 'building profile' }],
        event_count: 1,
      }));
    await openTaskMonitor({ tab: 'planner' });
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(0);
    let overlay = document.getElementById('task-monitor-overlay')!;
    expect(overlay.querySelector('.transcript-text')?.textContent).toContain('building profile');
    await vi.advanceTimersByTimeAsync(2000);
    overlay = document.getElementById('task-monitor-overlay')!;
    expect(overlay.querySelector('.task-monitor-empty')).toBeNull();
    expect(overlay.querySelector('.transcript-text')?.textContent).toContain('building profile');
    expect(taskInspectMock).toHaveBeenLastCalledWith('task-1', 0);
  });

  it('preserves collapsed tool state across incremental polls', async () => {
    taskInspectMock
      .mockResolvedValueOnce(makeInspect({
        transcript: [{ kind: 'tool', tool_id: 'call-1', tool_name: 'exec', tool_args: '{}', tool_result: 'ok', tool_status: 'completed' }],
        event_count: 2,
      }))
      .mockResolvedValueOnce(makeInspect({ transcript: [{ kind: 'tool', tool_id: 'call-1', tool_name: 'exec', tool_args: '{}', tool_result: 'ok', tool_status: 'completed' }], event_count: 2 }));
    await openTaskMonitor({ tab: 'planner' });
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(0);
    const tool = document.querySelector('.tool-activity') as HTMLElement;
    expect(tool.classList.contains('is-open')).toBe(false);
    tool.click();
    expect(tool.classList.contains('is-open')).toBe(true);
    tool.click();
    expect(tool.classList.contains('is-open')).toBe(false);
    await vi.advanceTimersByTimeAsync(2000);
    expect(tool.classList.contains('is-open')).toBe(false);
  });

  it('does not auto-scroll when the reader has scrolled away from the bottom', async () => {
    taskInspectMock
      .mockResolvedValueOnce(makeInspect({
        transcript: [{ kind: 'text', text: 'line one' }],
        event_count: 1,
      }))
      .mockResolvedValueOnce(makeInspect({
        transcript: [{ kind: 'text', text: 'line two' }],
        event_count: 2,
      }));
    await openTaskMonitor({ tab: 'planner' });
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(0);
    const body = document.querySelector('.task-monitor-body') as HTMLElement;
    Object.defineProperty(body, 'scrollHeight', { value: 1200, configurable: true });
    Object.defineProperty(body, 'clientHeight', { value: 400, configurable: true });
    body.scrollTop = 100;
    body.dispatchEvent(new Event('scroll'));
    await vi.advanceTimersByTimeAsync(2000);
    expect(body.scrollTop).toBe(100);
  });
});
