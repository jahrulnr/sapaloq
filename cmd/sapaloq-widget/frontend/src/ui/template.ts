// The root #app markup for the dock (orb + popup chat panel). Kept as a single
// template so main.ts stays a thin wiring/bootstrap module.
import { ICON_EXPAND, ICON_SEND } from './icons';

export const APP_TEMPLATE = `
  <div class="dock">
    <section class="popup" id="popup" aria-hidden="true" style="--wails-draggable: no-drag">
      <header class="popup-header">
        <div class="popup-brand">
          <span class="brand-mark" aria-hidden="true"><span class="brand-mark-core"></span></span>
          <span class="brand-copy"><span class="popup-name">SapaLOQ</span></span>
        </div>
        <div class="popup-header-right">
          <span class="context-usage" id="context-usage" data-level="normal" title="context usage">0/0</span>
          <span class="conn-pill"><span class="conn-dot" id="conn-dot" data-state="connecting" aria-label="status koneksi" title="menghubungkan…"></span><span>core</span></span>
          <button type="button" class="popup-resize" id="btn-resize" aria-label="Ubah ukuran chat" title="Ubah ukuran chat">□</button>
          <button type="button" class="popup-close" id="btn-close" aria-label="Tutup">×</button>
        </div>
      </header>
      <section class="runtime-strip" aria-label="Runtime status">
        <div class="runtime-model" title="Model aktif">
          <span class="runtime-kicker">MODEL</span>
          <strong id="runtime-model-name">loading…</strong>
          <span id="runtime-provider">provider</span>
        </div>
        <div class="runtime-actors" id="runtime-actors">
          <article class="actor-tile" data-state="idle"><span class="actor-signal"></span><span><b>Planner</b><small>idle</small></span></article>
          <article class="actor-tile" data-state="idle"><span class="actor-signal"></span><span><b>Agent</b><small>idle</small></span></article>
        </div>
        <div class="runtime-workspace" id="runtime-workspace" title="Workspace aktif">
          <span class="runtime-kicker">WORKSPACE</span>
          <strong>~/SapaLOQ/workspace</strong>
        </div>
      </section>
      <div class="popup-body">
        <div class="message-list" id="message-list"></div>
      </div>
      <footer class="popup-compose">
        <div class="compose-wrap" id="compose-wrap">
          <div class="slash-popover" id="slash-popover"></div>
          <button type="button" class="compose-expand" id="compose-expand" aria-label="Perbesar input" title="Perbesar input" aria-pressed="false">${ICON_EXPAND}</button>
          <div class="compose-row">
            <button type="button" class="attach-btn" id="attach-btn" aria-label="Attach file" title="Attach file"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14M5 12h14"/></svg></button>
            <div id="compose-input" class="compose-input" contenteditable="true" role="textbox" aria-multiline="true" data-placeholder="Ask anything"></div>
            <button type="button" class="send-btn" id="send-btn" aria-label="Kirim">${ICON_SEND}</button>
          </div>
          <input type="file" id="attach-input" class="attach-input" multiple aria-hidden="true" tabindex="-1">
        </div>
      </footer>
    </section>
    <div class="fab-row"><button type="button" class="orb" id="orb" data-state="idle" aria-label="Buka SapaLOQ" style="--wails-draggable: drag"><span class="orb-aura" aria-hidden="true"></span><span class="orb-ring" aria-hidden="true"></span><span class="orb-body" aria-hidden="true"><span class="orb-grid" aria-hidden="true"></span><span class="sapa-glyph" aria-hidden="true"><span class="glyph-node glyph-node--a"></span><span class="glyph-node glyph-node--b"></span><span class="glyph-node glyph-node--c"></span><span class="glyph-path glyph-path--a"></span><span class="glyph-path glyph-path--b"></span></span><span class="orb-specular" aria-hidden="true"></span><span class="ring-badge" id="ring-badge" aria-hidden="true"></span><span class="orb-chevron" aria-hidden="true">⌄</span></span></button></div>
  </div>
`;
