import { describe, it, expect } from 'vitest';
import { buildMergedTimeline } from './history';
import type { ChatTurn, StreamEvent } from '../core/types';

describe('buildMergedTimeline', () => {
  it('interleaves tool calls between chat turns by timestamp', () => {
    const turns: ChatTurn[] = [
      { id: 1, seq: 1, role: 'user', content: 'hi', created_at: '2026-06-26T19:14:20.000Z' },
      { id: 2, seq: 2, role: 'assistant', content: 'ok', created_at: '2026-06-26T19:14:25.000Z' },
    ];
    const events: StreamEvent[] = [
      { kind: 'tool_call', tool_call: { name: 'sapaloq_spawn_plan' }, at: '2026-06-26T19:14:22.000Z' },
      { kind: 'task_update', task_id: 't1', task_role: 'planner', task_status: 'done', at: '2026-06-26T19:14:24.000Z' },
    ];
    const merged = buildMergedTimeline(turns, events);
    expect(merged.map((item) => (item.kind === 'turn' ? item.turn.role : item.event.kind))).toEqual([
      'user',
      'tool_call',
      'task_update',
      'assistant',
    ]);
  });
});
