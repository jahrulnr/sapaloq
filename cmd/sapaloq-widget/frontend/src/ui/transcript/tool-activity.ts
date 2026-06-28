import type { ActivityEntry } from './types';

export type ToolActivityCall = {
  id?: string;
  name?: string;
  arguments?: unknown;
  source?: string;
};

export type ToolActivityMode = 'chat' | 'monitor';

export function formatToolPayload(value: unknown): string {
  if (value === undefined || value === null || value === '') return '';
  if (typeof value === 'string') {
    const trimmed = value.trim();
    if (!trimmed) return '';
    try {
      return JSON.stringify(JSON.parse(trimmed), null, 2);
    } catch {
      return value;
    }
  }
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

export function toolActivityHint(call: ToolActivityCall): string {
  const args = call.arguments;
  if (typeof args === 'object' && args !== null) {
    const record = args as Record<string, unknown>;
    if (typeof record.command === 'string' && record.command.trim()) return record.command.trim();
    if (typeof record.path === 'string' && record.path.trim()) return record.path.trim();
    const compact = formatToolPayload(args).replace(/\s+/g, ' ').trim();
    if (!compact) return '';
    return compact.length <= 72 ? compact : `${compact.slice(0, 69)}…`;
  }
  if (typeof args === 'string' && args.trim()) {
    const text = args.trim();
    return text.length <= 72 ? text : `${text.slice(0, 69)}…`;
  }
  return '';
}

export function toolPayloadSection(label: string, value: string, state = ''): HTMLElement {
  const section = document.createElement('section');
  section.className = 'tool-activity__section';
  if (state) section.dataset.state = state;
  const heading = document.createElement('span');
  heading.className = 'tool-activity__section-label';
  heading.textContent = label;
  const pre = document.createElement('pre');
  const code = document.createElement('code');
  code.textContent = value || (state === 'pending' ? 'Waiting for response…' : '(no output)');
  pre.append(code);
  section.append(heading, pre);
  return section;
}

export function setToolActivityOpen(item: HTMLElement, open: boolean, body?: HTMLElement) {
  item.classList.toggle('is-open', open);
  item.setAttribute('aria-expanded', String(open));
  const sink = body || item.querySelector<HTMLElement>('.tool-activity__body');
  if (sink) sink.hidden = !open;
}

export function displayToolName(name: string): string {
  switch (name) {
    case 'commandExecution': return 'shell';
    case 'fileChange': return 'edit';
    case 'webSearch': return 'web_search';
    case 'mcpToolCall': return 'mcp';
    case 'imageView': return 'image';
    case 'imageGeneration': return 'image_gen';
    default: return name;
  }
}

export function paintToolActivityHeader(item: HTMLElement, header: Text) {
  const marker = item.classList.contains('is-open') ? '⌄' : '›';
  const hint = item.dataset.toolHint ? `  ${item.dataset.toolHint}` : '';
  const name = displayToolName(item.dataset.toolName || 'unknown');
  header.nodeValue = `${marker}  $ ${name}${hint}  ·  ${item.dataset.toolStatus || 'running'}`;
}

export function getToolActivityHeader(item: HTMLElement): Text | null {
  const node = item.firstChild;
  return node?.nodeType === 3 ? node as Text : null;
}

function wireToolToggle(item: HTMLElement, body: HTMLElement, header: Text) {
  const toggle = () => {
    const open = !item.classList.contains('is-open');
    setToolActivityOpen(item, open);
    paintToolActivityHeader(item, header);
  };
  item.addEventListener('click', toggle);
  item.addEventListener('keydown', (event) => {
    if (event.target !== item) return;
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      toggle();
    }
  });
  body.addEventListener('click', (event) => event.stopPropagation());
}

export type CreateToolActivityOptions = {
  mode?: ToolActivityMode;
  extraClass?: string;
};

/** Build one Cursor-like tool disclosure row (shared by chat + monitor). */
export function createToolActivityElement(
  entry: Extract<ActivityEntry, { kind: 'tool' }>,
  options: CreateToolActivityOptions = {},
): HTMLElement {
  const waiting = entry.status === 'running' || entry.response === undefined;
  const item = document.createElement('div');
  item.className = ['tool-activity', options.extraClass].filter(Boolean).join(' ');
  item.dataset.entryKind = 'tool';
  item.dataset.toolName = entry.name;
  item.dataset.toolStatus = entry.status || (waiting ? 'running' : 'completed');
  if (entry.id) item.dataset.toolId = entry.id;
  if (!waiting) item.dataset.complete = 'true';

  item.setAttribute('role', 'button');
  item.setAttribute('tabindex', '0');

  const header = document.createTextNode('');
  const body = document.createElement('div');
  body.className = 'tool-activity__body';
  body.append(toolPayloadSection('Request', formatToolPayload(entry.args) || 'No arguments'));
  const streaming = entry.status === 'running' && entry.response !== undefined && entry.response !== '';
  if (waiting && !streaming) {
    body.append(toolPayloadSection('Response', '', 'pending'));
  } else {
    const state = entry.status === 'failed' ? 'error' : entry.status === 'running' ? 'pending' : 'complete';
    body.append(toolPayloadSection('Response', formatToolPayload(entry.response || ''), state));
  }

  wireToolToggle(item, body, header);
  item.append(header, body);

  const open = false;
  setToolActivityOpen(item, open, body);
  paintToolActivityHeader(item, header);
  return item;
}

export function toolEntryFromCall(call: ToolActivityCall): Extract<ActivityEntry, { kind: 'tool' }> {
  return {
    kind: 'tool',
    id: call.id || '',
    name: call.name || 'unknown',
    args: formatToolPayload(call.arguments),
  };
}

export function patchToolActivityElement(
  item: HTMLElement,
  entry: Extract<ActivityEntry, { kind: 'tool' }>,
) {
  const waiting = entry.status === 'running' || entry.response === undefined;
  item.dataset.toolName = entry.name;
  item.dataset.toolStatus = entry.status || (waiting ? 'running' : 'completed');
  if (entry.id) item.dataset.toolId = entry.id;
  if (!waiting) item.dataset.complete = 'true';
  else delete item.dataset.complete;

  const header = getToolActivityHeader(item);
  if (header) paintToolActivityHeader(item, header);

  const sections = item.querySelectorAll('.tool-activity__section');
  if (sections.length >= 1) {
    const request = sections[0].querySelector('code');
    if (request) request.textContent = formatToolPayload(entry.args) || 'No arguments';
  }

  if (entry.response === undefined && entry.status !== 'running') return;

  const state = entry.status === 'failed' ? 'error' : entry.status === 'running' ? 'pending' : 'complete';
  const response = formatToolPayload(entry.response);
  let responseSection = sections[1] as HTMLElement | undefined;
  if (!responseSection) {
    const body = item.querySelector<HTMLElement>('.tool-activity__body');
    if (body) {
      responseSection = toolPayloadSection('Response', '', 'pending');
      body.append(responseSection);
    }
  }
  if (!responseSection) return;
  const code = responseSection.querySelector('code');
  if (code) code.textContent = response || (state === 'pending' ? 'Waiting for response…' : '(no output)');
  responseSection.dataset.state = state;
  setToolActivityOpen(item, false);
  if (header) paintToolActivityHeader(item, header);
}
