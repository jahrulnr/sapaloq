// notifications.ts fires a system notification + chime when an orchestrator
// run or a sub-agent (planner/agent) reaches a terminal state. The chime is
// role-specific: planner completions play notification-planner.wav, agent
// (task-runner) completions play notification-agent.wav, and the foreground
// orchestrator run plays the generic notification.wav. Each is fetched once as
// a data: URI from the Go side and cached so the browser can replay it without
// a network round-trip per completion.
//
// System notifications go through the Wails runtime (SendNotification), which
// routes to notify-send / NSUserNotificationCenter / Windows toast depending
// on platform. Everything degrades silently in a plain browser (no Wails
// runtime): the sound still plays, the toast is skipped.

import { NotificationSound, NotificationSoundForRole } from '../../wailsjs/go/main/App';
import * as runtime from '../../wailsjs/runtime/runtime';

// Audio cache keyed by the chime "slot": 'orchestrator' (generic) or a role
// name ('planner' / 'task-runner'). Each slot is fetched once and replayed via
// currentTime rewind so a rapid burst of completions doesn't allocate a
// decoder per ding.
type AudioSlot = HTMLAudioElement | null;
const audioCache = new Map<string, AudioSlot>();
const audioLoading = new Map<string, Promise<AudioSlot>>();
// Roles whose chime we proactively prime at bootstrap. 'task-runner' is the
// orchestrator-internal role name; 'agent' is accepted as an alias.
const PRIMED_ROLES = ['planner', 'task-runner'];

let notificationsReady = false;
let notificationsInitAttempted = false;
// Incrementing id so each notification is a fresh toast (some platforms
// collapse identical ids into a silent update of an existing banner).
let notificationSeq = 0;

// slotKeyFor resolves the audio cache key for a completion. Orchestrator runs
// use the generic chime; task completions use their role's chime, falling back
// to the generic slot for unknown roles.
function slotKeyFor(kind: 'orchestrator' | 'task', role?: string): string {
  if (kind === 'orchestrator') return 'orchestrator';
  const r = (role || '').toLowerCase();
  if (r === 'planner' || r === 'task-runner' || r === 'agent') return r;
  return 'orchestrator';
}

// fetchAudioURI pulls the data: URI for a slot from the Go side.
async function fetchAudioURI(slot: string): Promise<string> {
  if (slot === 'orchestrator') return NotificationSound();
  // The Go side accepts 'task-runner' / 'agent' interchangeably for the agent
  // chime; normalize so the cache key stays the role the chat controller sent.
  return NotificationSoundForRole(slot === 'agent' ? 'task-runner' : slot);
}

// loadAudio fetches + caches the HTMLAudioElement for a slot. Concurrent calls
// for the same slot share the in-flight load.
function loadAudio(slot: string): Promise<AudioSlot> {
  const cached = audioCache.get(slot);
  if (cached !== undefined) return Promise.resolve(cached);
  const loading = audioLoading.get(slot);
  if (loading) return loading;
  const p = (async () => {
    try {
      const uri = await fetchAudioURI(slot);
      if (!uri) {
        audioCache.set(slot, null);
        return null;
      }
      const el = new Audio(uri);
      el.preload = 'auto';
      audioCache.set(slot, el);
      return el;
    } catch {
      audioCache.set(slot, null);
      return null;
    } finally {
      audioLoading.delete(slot);
    }
  })();
  audioLoading.set(slot, p);
  return p;
}

// ensureNotifications initializes the Wails notification service + (on macOS)
// requests authorization. Best-effort: any failure just means we fall back to
// a chime-only completion. Idempotent.
async function ensureNotifications() {
  if (notificationsReady || notificationsInitAttempted) return;
  notificationsInitAttempted = true;
  try {
    const available = await runtime.IsNotificationAvailable();
    if (!available) return;
    await runtime.InitializeNotifications();
    // macOS requires explicit authorization; on other platforms this is a
    // no-op that resolves true.
    try {
      const ok = await runtime.RequestNotificationAuthorization();
      if (ok === false) return;
    } catch {
      /* non-macOS: ignore */
    }
    notificationsReady = true;
  } catch {
    /* no Wails runtime in a plain browser */
  }
}

// playChime replays the cached chime for a slot. Errors (e.g. autoplay policy
// before a user gesture) are swallowed — the system toast is the primary
// signal.
async function playChime(slot: string) {
  const el = await loadAudio(slot);
  if (!el) return;
  try {
    el.pause();
    el.currentTime = 0;
    await el.play();
  } catch {
    /* autoplay blocked or decode error — silent */
  }
}

// notifyCompletion is the single entry point used by the chat controller. It
// always plays the role-appropriate chime (best-effort) and, when the Wails
// runtime is present, posts a native toast. `kind` is a stable id namespace so
// an orchestrator "done" and a per-task "done" never share a toast id; `role`
// (for kind:'task') selects the planner/agent chime.
export async function notifyCompletion(kind: 'orchestrator' | 'task', title: string, body: string, role?: string) {
  void playChime(slotKeyFor(kind, role));
  await ensureNotifications();
  if (!notificationsReady) return;
  try {
    await runtime.SendNotification({
      id: `${kind}:${++notificationSeq}`,
      title,
      body,
    });
  } catch {
    /* swallow — the chime already fired */
  }
}

// Eagerly prime the audio buffers so the first completion isn't delayed by the
// data-URI fetch. Safe to call at bootstrap; failures are silent. Primes the
// generic + both role chimes so whichever finishes first dings instantly.
export function primeNotifications() {
  void loadAudio('orchestrator');
  for (const role of PRIMED_ROLES) void loadAudio(role);
  void ensureNotifications();
}
