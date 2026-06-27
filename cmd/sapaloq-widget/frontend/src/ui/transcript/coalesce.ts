import type { ActivityEntry, StreamLikeEvent } from './types';
import { isAutopilotNudge } from './autopilot';

/** Merge a raw progress/event stream into display-ready transcript entries. */
export function coalesceEvents(events: StreamLikeEvent[]): ActivityEntry[] {
  const out: ActivityEntry[] = [];
  let textBuf = '';
  let thinkBuf = '';
  const flushText = () => {
    if (textBuf.trim()) out.push({ kind: 'text', text: textBuf });
    textBuf = '';
  };
  const flushThinking = () => {
    if (thinkBuf.trim()) out.push({ kind: 'thinking', text: thinkBuf });
    thinkBuf = '';
  };
  for (const ev of events) {
    switch (ev.kind) {
      case 'response_delta':
        flushThinking();
        textBuf += ev.delta || '';
        break;
      case 'thinking_delta':
        flushText();
        thinkBuf += ev.delta || '';
        break;
      case 'tool_call':
        flushText();
        flushThinking();
        out.push({
          kind: 'tool',
          id: ev.tool_id || '',
          name: ev.tool_name || 'tool',
          args: ev.tool_arguments || '',
        });
        break;
      case 'tool_update': {
        flushText();
        flushThinking();
        const match = [...out].reverse().find((item): item is Extract<ActivityEntry, { kind: 'tool' }> =>
          item.kind === 'tool'
          && (ev.tool_id ? item.id === ev.tool_id : item.name === (ev.tool_name || 'tool'))
          && item.response === undefined);
        if (match) {
          match.response = ev.tool_result || ev.error || '';
          match.status = ev.error ? 'failed' : (ev.status || 'completed');
        } else {
          out.push({
            kind: 'tool',
            id: ev.tool_id || '',
            name: ev.tool_name || 'tool',
            args: ev.tool_arguments || '',
            response: ev.tool_result || ev.error || '',
            status: ev.error ? 'failed' : (ev.status || 'completed'),
          });
        }
        break;
      }
      case 'turn_boundary':
      case 'task_update':
        flushText();
        flushThinking();
        break;
      case 'status':
        if (ev.status && ev.status !== 'working') {
          flushText();
          flushThinking();
          if (isAutopilotNudge(ev.status)) {
            out.push({ kind: 'user', text: ev.status });
          } else {
            out.push({ kind: 'status', label: ev.status });
          }
        }
        break;
      case 'error':
        flushText();
        flushThinking();
        out.push({ kind: 'status', label: 'error: ' + (ev.error || 'unknown') });
        break;
      default:
        break;
    }
  }
  flushText();
  flushThinking();
  return out;
}
