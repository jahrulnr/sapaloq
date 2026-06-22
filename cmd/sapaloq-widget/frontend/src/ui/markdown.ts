// Markdown is rendered with the `marked` library (GFM: headings, tables, lists,
// code fences, blockquotes, etc.) and sanitized with DOMPurify before it ever
// touches the DOM. This replaces the previous hand-rolled parser, which could
// not handle GFM tables and mis-rendered headings glued to following content.
import { marked } from 'marked';
import DOMPurify from 'dompurify';
import { OpenExternal } from '../../wailsjs/go/main/App';
import { sanitizeDisplayText } from './dom';
import { showImagePreview } from './image-preview';

marked.setOptions({
  gfm: true,
  breaks: true, // preserve the old single-newline => <br> behaviour
});

// Open links + keep our image-preview affordance after sanitizing. WebKitGTK
// ignores target=_blank/window.open, so anchor clicks are routed Go-side via
// OpenExternal (http→browser, abs path/file:→file manager). target/rel are kept
// for plain-browser environments where the native binding is absent.
function decorateRenderedMarkdown(root: ParentNode) {
  root.querySelectorAll('a[href]').forEach((node) => {
    const a = node as HTMLAnchorElement;
    a.target = '_blank';
    a.rel = 'noreferrer';
    a.addEventListener('click', (event) => {
      const href = a.getAttribute('href') || '';
      if (!href || href.startsWith('#')) return;
      event.preventDefault();
      try {
        void OpenExternal(href);
      } catch {
        try { window.open(href, '_blank'); } catch { /* no-op */ }
      }
    });
  });
  root.querySelectorAll('img').forEach((node) => {
    const img = node as HTMLImageElement;
    img.classList.add('message-image');
    img.loading = 'lazy';
    img.addEventListener('click', () => showImagePreview(img.src, img.alt || 'image'));
  });
  // Keep existing heading/quote/code styling hooks the stylesheet relies on.
  root.querySelectorAll('h1,h2,h3,h4,h5,h6').forEach((h) => h.classList.add('md-heading'));
  root.querySelectorAll('blockquote').forEach((q) => q.classList.add('md-quote'));
  root.querySelectorAll('pre').forEach((p) => p.classList.add('code-block'));
}

export function renderMarkdown(text: string): DocumentFragment {
  const safeText = sanitizeDisplayText(text);
  const rawHTML = marked.parse(safeText, { async: false }) as string;
  const clean = DOMPurify.sanitize(rawHTML, {
    ADD_ATTR: ['target', 'rel'],
    // Allow http(s)/mailto/tel + data:image (inline previews) AND local file
    // references — `file:` URLs and absolute paths (`/…`) — so a `[name](/tmp/x)`
    // link keeps its href and stays clickable (routed via OpenExternal).
    // Everything else (notably `javascript:`) is still stripped.
    ALLOWED_URI_REGEXP: /^(?:(?:https?|mailto|tel|file):|data:image\/|\/)/i,
  });
  const template = document.createElement('template');
  template.innerHTML = clean;
  decorateRenderedMarkdown(template.content);
  return template.content;
}
