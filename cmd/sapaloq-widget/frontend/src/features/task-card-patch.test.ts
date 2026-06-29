import { beforeEach, describe, expect, it } from 'vitest';
import { APP_TEMPLATE } from '../ui/template';
import { patchForegroundTaskCards } from './transcript-pane';
import { createToolActivityElement } from '../ui/transcript';

describe('patchForegroundTaskCards', () => {
  beforeEach(() => {
    document.body.innerHTML = `<div id="app">${APP_TEMPLATE}</div>`;
    const list = document.getElementById('message-list')!;
    const pane = document.createElement('div');
    pane.className = 'transcript-pane';
    const tool = createToolActivityElement(
      { kind: 'tool', id: 'tc-1', name: 'sapaloq_spawn_plan', args: '{}', status: 'completed', response: 'ok' },
      { mode: 'chat', extraClass: 'message' },
    );
    tool.dataset.entryKind = 'tool';
    pane.append(tool);
    list.append(pane);
  });

  it('updates task cards without removing existing tool rows', () => {
    patchForegroundTaskCards([{
      kind: 'task',
      id: 'task-planner-1',
      task_id: 'task-planner-1',
      task_role: 'planner',
      task_status: 'in_progress',
      summary: 'Auditing UI',
    }]);

    const pane = document.querySelector('.transcript-pane')!;
    expect(pane.querySelectorAll('[data-entry-kind="tool"]')).toHaveLength(1);
    expect(pane.querySelector('[data-task-id="task-planner-1"]')).not.toBeNull();
  });
});
