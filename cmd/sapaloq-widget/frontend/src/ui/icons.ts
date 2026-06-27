// Inline stroke icons (no external sprite/font dependency). `fill:none` +
// currentColor styling lives in style.css under .send-btn svg; send is an
// arrow-up (à la ChatGPT), stop is a rounded square shown while a response is
// streaming.
export const ICON_SEND = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 19V5M6 11l6-6 6 6"/></svg>';
export const ICON_STOP = '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="7" y="7" width="10" height="10" rx="2.5"/></svg>';
// Diagonal "expand" chevrons (↗ + ↙ corners) ↔ "collapse" inward arrows.
export const ICON_EXPAND = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M14 4h6v6M20 4l-7 7M10 20H4v-6M4 20l7-7"/></svg>';
export const ICON_COLLAPSE = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 10h-6V4M14 10l6-6M4 14h6v6M10 14l-6 6"/></svg>';
// History = clock with a counter-clockwise arrow; caret = the small chevron in
// the switcher button; new-chat = a compose pencil for starting a fresh chat.
export const ICON_HISTORY = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 12a9 9 0 1 0 3-6.7L3 8M3 3v5h5M12 8v4l3 2"/></svg>';
export const ICON_CARET = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 9l6 6 6-6"/></svg>';
export const ICON_NEW_CHAT = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5H6a2 2 0 0 0-2 2v11a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2v-6M18.5 3.5a2.1 2.1 0 0 1 3 3L13 15l-4 1 1-4z"/></svg>';
