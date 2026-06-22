import { describe, it, expect, vi } from 'vitest';

// The Wails-generated binding calls window.go.* at invocation time. It's only
// used inside click handlers (never at render time), but mock it so importing
// markdown.ts never depends on a live runtime.
vi.mock('../../wailsjs/go/main/App', () => ({ OpenExternal: vi.fn() }));

import { renderMarkdown } from './markdown';

function render(md: string): HTMLElement {
  const host = document.createElement('div');
  host.append(renderMarkdown(md));
  return host;
}

describe('renderMarkdown — GFM task lists', () => {
  // Regression for the "checkbox jadi input panjang" bug: marked emits
  // `<input disabled type="checkbox">`, but DOMPurify strips `type` unless it
  // is explicitly allowed — leaving a bare `<input>` the browser renders as a
  // long text field. The fix is ADD_ATTR: ['...', 'type'].
  it('keeps the checkbox type so it renders as a checkbox, not a text input', () => {
    const host = render('- [ ] todo item\n- [x] done item\n');
    const inputs = host.querySelectorAll('input');
    expect(inputs.length).toBe(2);
    inputs.forEach((input) => {
      expect(input.getAttribute('type')).toBe('checkbox');
    });
  });

  it('marks the [x] item checked and the [ ] item unchecked', () => {
    const host = render('- [ ] open\n- [x] closed\n');
    const inputs = host.querySelectorAll('input[type="checkbox"]');
    expect(inputs.length).toBe(2);
    expect((inputs[0] as HTMLInputElement).hasAttribute('checked')).toBe(false);
    expect((inputs[1] as HTMLInputElement).hasAttribute('checked')).toBe(true);
  });

  it('preserves the item text alongside the checkbox', () => {
    const host = render('- [ ] **Struktur direktori** di `/tmp/webprofile`\n');
    expect(host.textContent).toContain('Struktur direktori');
    expect(host.querySelector('strong')?.textContent).toBe('Struktur direktori');
    expect(host.querySelector('code')?.textContent).toBe('/tmp/webprofile');
  });

  it('keeps the item content intact when it has bold/code and a nested sublist', () => {
    // Regression for the "ke-squeeze jadi kolom sempit" bug: a task item that
    // also has a nested sublist must keep its checkbox + inline content + the
    // nested <ul> as direct children of the same <li> (so it lays out as one
    // flowing line with the sublist below, not as squeezed columns).
    const host = render(
      '- [ ] **Struktur direktori** di `/tmp/webprofile`:\n  - `index.html` — markup\n  - `style.css` — tema\n',
    );
    const item = host.querySelector('li.md-task-item');
    expect(item).not.toBeNull();
    // checkbox is present and is the leading child
    expect(item?.querySelector('input.md-task-check')?.getAttribute('type')).toBe('checkbox');
    // inline bold + code preserved on the item itself
    expect(item?.querySelector(':scope > strong')?.textContent).toBe('Struktur direktori');
    expect(item?.querySelector(':scope > code')?.textContent).toBe('/tmp/webprofile');
    // the nested sublist is a child of THIS item and has both sub-entries
    const sublist = item?.querySelector(':scope > ul');
    expect(sublist).not.toBeNull();
    expect(sublist?.querySelectorAll('li').length).toBe(2);
    // full readable text is preserved (not dropped/garbled)
    expect(item?.textContent).toContain('Struktur direktori');
    expect(item?.textContent).toContain('/tmp/webprofile');
  });

  it('still strips dangerous markup (sanitization intact)', () => {
    const host = render('<img src=x onerror="alert(1)"> ok\n');
    const img = host.querySelector('img');
    // onerror must be removed even though we allow more attributes now.
    expect(img?.getAttribute('onerror')).toBeNull();
  });
});
