# JavaScript Style Guide

> **Note:** If your project supports TypeScript, use `typescript.md` instead.
> This guide applies to plain `.js` / `.mjs` scripts, build tool configs,
> and environments where TypeScript is not available.

Based on the Google JavaScript Style Guide, updated for 2024+.

---

## 1. Tooling Setup

Both are required. They have different roles — don't skip either.

### Prettier (`.prettierrc`)
```json
{
  "semi": true,
  "singleQuote": true,
  "trailingComma": "all",
  "printWidth": 100,
  "tabWidth": 2
}
```

### ESLint (`eslint.config.js`)
```js
import js from '@eslint/js';
import prettier from 'eslint-config-prettier';

export default [
  js.configs.recommended,
  prettier,
  {
    rules: {
      'no-var': 'error',
      'prefer-const': 'error',
      'eqeqeq': ['error', 'always'],
      'no-console': ['warn', { allow: ['warn', 'error'] }],
      'no-eval': 'error',
    },
  },
];
```

---

## 2. Variables

- Use `const` by default. Use `let` only when reassignment is needed.
- `var` is **forbidden** — it has function scope, not block scope, and hoisting
  behavior causes bugs.
  ```js
  // ✅
  const maxRetries = 3;
  let attempt = 0;

  // ❌
  var maxRetries = 3;
  ```

---

## 3. Formatting

- **Indentation:** 2 spaces. Never tabs.
- **Line length:** 100 characters max. (Google says 80, but 100 is the
  practical standard for modern screens.)
- **Semicolons:** Required after every statement. Don't rely on ASI.
- **Braces:** K&R style — opening brace on the same line.
  ```js
  // ✅
  if (condition) {
    doSomething();
  }

  // ❌ — omitting braces for single-line ifs causes bugs
  if (condition) doSomething();
  ```
- **Trailing commas** in multi-line arrays, objects, function params.

---

## 4. Modules

- Use ES modules (`import`/`export`). Never CommonJS `require()` in new code.
- **Named exports** preferred over default exports.
- Include `.js` extension in import paths (required for native ESM):
  ```js
  import { parse } from './config.js';  // ✅
  import { parse } from './config';     // ❌ in native ESM
  ```

---

## 5. Functions

- **Arrow functions** for callbacks and anonymous functions.
- **`function` declarations** for named, top-level functions.
- **`async/await`** over `.then()` chains.
  ```js
  // ✅
  async function fetchData(url) {
    const res = await fetch(url);
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    return res.json();
  }
  ```
- Avoid `this` outside of class methods. Bind explicitly if needed or use an
  arrow function instead.

---

## 6. Strings

- Single quotes `'` for string literals.
- Template literals `` ` `` for interpolation and multi-line strings.
- No string concatenation with `+` when template literals work.

---

## 7. Equality

- Always `===` and `!==`. Never `==` or `!=`.

---

## 8. Naming

| Kind | Convention |
|---|---|
| Classes | `UpperCamelCase` |
| Functions, variables | `lowerCamelCase` |
| Constants (module-level) | `CONSTANT_CASE` |
| Files | `kebab-case.js` |

---

## 9. Disallowed

- `eval()` and `new Function(...)` — security risk, no exceptions.
- `with` — deprecated, confusing scoping.
- Modifying built-in prototypes (`Array.prototype.foo = ...`).
- Default exports (prefer named) — exception: scripts with a single entry point.

---

## 10. JSDoc (for untyped JS)

When working in plain JS without TypeScript, use JSDoc to document types:
```js
/**
 * @param {string} userId
 * @param {{ timeout?: number }} [opts]
 * @returns {Promise<User>}
 */
async function getUser(userId, opts = {}) { ... }
```

*Primary source: [Google JavaScript Style Guide](https://google.github.io/styleguide/jsguide.html)*
