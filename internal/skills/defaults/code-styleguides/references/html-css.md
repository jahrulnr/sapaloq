# HTML/CSS Style Guide

Based on the Google HTML/CSS Style Guide, augmented with accessibility
requirements, CSS custom properties conventions, and modern component-based
approaches.

> **Tailwind users:** See the Tailwind-specific section at the end. The general
> HTML rules still apply; most CSS rules are replaced by Tailwind conventions.

---

## 1. General

- **Encoding:** UTF-8. Always declare `<meta charset="utf-8">`.
- **Indentation:** 2 spaces. Never tabs.
- **Capitalization:** Lowercase for all element names, attributes, selectors,
  and property names.
- **Trailing whitespace:** None.
- **Protocol:** HTTPS for all embedded resources (`src`, `href`, `action`).

---

## 2. HTML Rules

### Document structure
```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>Page title</title>
    <link rel="stylesheet" href="styles.css">
  </head>
  <body>
    ...
    <script src="app.js"></script>
  </body>
</html>
```

- `lang` attribute on `<html>` is **required** for screen readers.
- `viewport` meta is **required** for responsive layouts.
- Omit `type` on `<link rel="stylesheet">` and `<script>` — it's the default.

### Semantics

Use elements for their intended purpose. This improves accessibility and SEO.

```html
<!-- ✅ semantic -->
<nav aria-label="Main navigation">
  <ul>
    <li><a href="/home">Home</a></li>
  </ul>
</nav>

<main>
  <article>
    <h1>Article title</h1>
    <p>Content...</p>
  </article>
</main>

<footer>...</footer>

<!-- ❌ div soup -->
<div class="nav">
  <div class="nav-item"><a href="/home">Home</a></div>
</div>
```

Key semantic elements: `<header>`, `<nav>`, `<main>`, `<article>`, `<section>`,
`<aside>`, `<footer>`, `<figure>`, `<figcaption>`, `<time>`, `<address>`.

### Multimedia

- Always provide `alt` for images. Empty `alt=""` for decorative images
  (screen readers skip them).
- Provide captions or transcripts for `<video>` and `<audio>`.

### Quotation marks

Use double quotes `"` for HTML attribute values:
```html
<img src="logo.png" alt="Company logo">    <!-- ✅ -->
<img src='logo.png' alt='Company logo'>    <!-- ❌ -->
```

---

## 3. Accessibility (ARIA)

Accessibility is not optional. These are the minimum requirements.

### Always include

- `lang` on `<html>`
- `alt` on `<img>` (meaningful text, not "image" or the filename)
- `<label>` for every form input, or `aria-label` / `aria-labelledby` if
  a visible label isn't possible
- Logical heading hierarchy: one `<h1>` per page, then `<h2>`, `<h3>` — no skipping
- Sufficient color contrast: 4.5:1 for text, 3:1 for large text and UI components

### Landmark roles

Browsers expose implicit ARIA roles for semantic elements:
- `<nav>` → `role="navigation"` (add `aria-label` if multiple navs exist)
- `<main>` → `role="main"` (only one per page)
- `<header>` inside `<body>` → `role="banner"`
- `<footer>` inside `<body>` → `role="contentinfo"`

### Interactive elements

- Buttons that open menus or modals: `aria-expanded="true/false"`,
  `aria-controls="target-id"`
- Toggles: `aria-pressed="true/false"`
- Modal dialogs: `role="dialog"`, `aria-modal="true"`, `aria-labelledby`
- Loading states: `aria-live="polite"` on the container that updates

### Keyboard navigation

- Every interactive element must be reachable by `Tab`.
- Custom interactive elements (non-`<button>` acting as a button): add
  `tabindex="0"` and handle `Enter`/`Space` keydown events.
- Focus must be visible — never `outline: none` without a custom visible
  focus style.

---

## 4. CSS — Naming

Use **BEM** (Block-Element-Modifier) for component class naming:
```
.block {}
.block__element {}
.block--modifier {}
.block__element--modifier {}
```

```css
/* Block */
.card {}

/* Elements */
.card__title {}
.card__body {}
.card__footer {}

/* Modifiers */
.card--featured {}
.card__title--large {}
```

Rules:
- Block and element names: `kebab-case`.
- Class names must be meaningful and generic. Describe the role, not the
  appearance.
  ```css
  .site-navigation {}   /* ✅ — role */
  .blue-text {}         /* ❌ — appearance */
  .nav {}               /* ❌ — too abbreviated */
  ```
- Avoid ID selectors for styling — they have high specificity and can't
  be reused.
- Avoid `!important` — it's a sign of a specificity battle. Fix the root cause.

---

## 5. CSS — Custom Properties (Variables)

Use CSS custom properties for all design tokens. Define them on `:root`:

```css
:root {
  /* Colors */
  --color-primary: #3b5bdb;
  --color-primary-hover: #364fc7;
  --color-text: #1a1a2e;
  --color-text-muted: #6c6c80;
  --color-surface: #ffffff;
  --color-border: #e2e8f0;

  /* Spacing */
  --space-1: 0.25rem;
  --space-2: 0.5rem;
  --space-4: 1rem;
  --space-8: 2rem;

  /* Typography */
  --font-sans: 'Inter', system-ui, sans-serif;
  --font-mono: 'JetBrains Mono', monospace;
  --text-sm: 0.875rem;
  --text-base: 1rem;
  --text-lg: 1.125rem;

  /* Radius */
  --radius-sm: 4px;
  --radius-md: 8px;
  --radius-lg: 12px;
}

@media (prefers-color-scheme: dark) {
  :root {
    --color-text: #e2e8f0;
    --color-surface: #1a1a2e;
    --color-border: #2d3748;
  }
}
```

Use variables in components — never hardcode hex colors:
```css
/* ✅ */
.btn-primary {
  background: var(--color-primary);
  color: var(--color-surface);
}

/* ❌ */
.btn-primary {
  background: #3b5bdb;
  color: #ffffff;
}
```

---

## 6. CSS — Formatting & Structure

- **One property per line.** Always end with a semicolon.
- **Space** before the opening brace: `.foo {` not `.foo{`.
- **New line** per selector when a rule has multiple selectors.
- **Separate rules** with a blank line.
- **Declaration order:** Group logically — box model first, then typography,
  then visual, then animation. Don't alphabetize (it separates logically
  related properties like `width` and `height`).
  ```css
  .card {
    /* Box model */
    display: flex;
    flex-direction: column;
    width: 100%;
    max-width: 400px;
    padding: 1rem;
    margin: 0 auto;

    /* Typography */
    font-family: var(--font-sans);
    font-size: var(--text-base);
    color: var(--color-text);

    /* Visual */
    background: var(--color-surface);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-md);
  }
  ```
- **Shorthand properties** where appropriate:
  ```css
  padding: 1rem 1.5rem;    /* ✅ shorthand */
  padding-top: 1rem;       /* only if you're overriding one side */
  ```
- **Zero values:** No unit needed: `margin: 0` not `margin: 0px`.
- **Leading zeros:** Always include: `opacity: 0.5` not `opacity: .5`.
- **Hex:** 3-character when possible: `#fff` not `#ffffff`.

---

## 7. CSS — Quotation Marks

Single quotes in CSS attribute selectors and property values:
```css
[type='text'] {}
@import url('fonts.css');
```
Double quotes in HTML attributes (see section 2).

---

## 8. Tailwind CSS

When using Tailwind, the utility-class approach replaces most of the CSS
naming and formatting rules above. The HTML and accessibility rules still apply.

- **Custom design tokens** still belong in `tailwind.config.js` (not inline `style`):
  ```js
  theme: {
    extend: {
      colors: { primary: '#3b5bdb' },
      borderRadius: { card: '12px' },
    },
  }
  ```
- Use `@apply` sparingly — only for repeated patterns that can't be
  extracted into a component.
- Never mix Tailwind utility classes with BEM class names on the same element.
- Keep the class attribute readable — group by concern:
  ```html
  <!-- Layout → spacing → typography → visual → state/interaction -->
  <button class="flex items-center gap-2 px-4 py-2 text-sm font-medium bg-primary text-white rounded-md hover:bg-primary-hover focus:outline-none focus:ring-2">
  ```

*Primary source: [Google HTML/CSS Style Guide](https://google.github.io/styleguide/htmlcssguide.html),
[MDN Accessibility Guide](https://developer.mozilla.org/en-US/docs/Web/Accessibility)*
