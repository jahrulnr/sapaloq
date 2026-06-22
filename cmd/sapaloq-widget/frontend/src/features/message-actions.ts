// Action registry that decouples message bubbles (messages.ts) from the chat
// controller (chat-controller.ts). The controller registers the turn-level
// actions (retry / delete / edit) at bootstrap; the message-wiring code calls
// them through these thin dispatchers, so neither module imports the other and
// there is no circular dependency.

type Handlers = {
  retry: (turnID: number) => void;
  delete: (turnID: number) => void;
  edit: (text: string) => void;
};

const handlers: Handlers = {
  retry: () => {},
  delete: () => {},
  edit: () => {},
};

export function registerMessageActions(next: Partial<Handlers>) {
  Object.assign(handlers, next);
}

export function retryTurn(turnID: number) { handlers.retry(turnID); }
export function deleteTurn(turnID: number) { handlers.delete(turnID); }
export function editText(text: string) { handlers.edit(text); }

export async function copyText(text: string) {
  if (!text) return;
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    // Fallback: copy via a throwaway off-screen textarea (the compose box is now
    // contenteditable and isn't a reliable execCommand('copy') source).
    const scratch = document.createElement('textarea');
    scratch.value = text;
    scratch.setAttribute('aria-hidden', 'true');
    scratch.style.position = 'fixed';
    scratch.style.left = '-9999px';
    document.body.appendChild(scratch);
    scratch.select();
    try { document.execCommand('copy'); } catch { /* no-op */ }
    scratch.remove();
  }
}
