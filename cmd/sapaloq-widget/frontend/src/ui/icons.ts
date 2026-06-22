// Inline stroke icons (no external sprite/font dependency). `fill:none` +
// currentColor styling lives in style.css under .send-btn svg; send is an
// arrow-up (à la ChatGPT), stop is a rounded square shown while a response is
// streaming.
export const ICON_SEND = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 19V5M6 11l6-6 6 6"/></svg>';
export const ICON_STOP = '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="7" y="7" width="10" height="10" rx="2.5"/></svg>';
// Diagonal "expand" chevrons (↗ + ↙ corners) ↔ "collapse" inward arrows.
export const ICON_EXPAND = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M14 4h6v6M20 4l-7 7M10 20H4v-6M4 20l7-7"/></svg>';
export const ICON_COLLAPSE = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 10h-6V4M14 10l6-6M4 14h6v6M10 14l-6 6"/></svg>';
