// Shared mutable session state. Centralising the few cross-cutting mutable
// values here (instead of file-scope `let`s in main.ts) lets the domain modules
// stay decoupled and avoids circular imports, while keeping a single source of
// truth for the in-flight turn / session bookkeeping.
import type { ConnectionState } from './types';
import type { ComposeBox } from '../ui/compose';

let _connection: ConnectionState = 'connecting';
let _submitting = false;
let _currentSessionID = '';
let _messageSeq = 0;
let _currentUserGroup = 0;
let _lastSubmittedText = '';
let _compose: ComposeBox | null = null;

export function getConnection() { return _connection; }
export function setConnectionState(v: ConnectionState) { _connection = v; }

export function isSubmitting() { return _submitting; }
export function setSubmitting(v: boolean) { _submitting = v; }

export function getSessionID() { return _currentSessionID; }
export function setSessionID(v: string) { _currentSessionID = v; }

export function getUserGroup() { return _currentUserGroup; }
export function setUserGroup(v: number) { _currentUserGroup = v; }
export function nextUserGroup() { return ++_currentUserGroup; }

export function nextMessageSeq() { return ++_messageSeq; }
export function resetMessageSeq() { _messageSeq = 0; }

export function getLastSubmittedText() { return _lastSubmittedText; }
export function setLastSubmittedText(v: string) { _lastSubmittedText = v; }

export function getCompose() { return _compose; }
export function setCompose(c: ComposeBox | null) { _compose = c; }

// Task ids whose spoken-completion bubble has already been rendered this
// session. The orchestrator stamps response_delta completions with task_id and
// may re-publish a terminal transition, so we render at most one bubble per
// task — preventing the duplicate "Task … selesai/gagal" assistant bubble.
export const spokenTaskIDs = new Set<string>();
export const taskBubbles = new Map<string, HTMLElement>();
export const taskStatuses = new Map<string, string>();
