---
name: code-styleguides
description: Opinionated, tooling-aware code style guides for writing, reviewing, or refactoring code. Use when generating code, doing a code review or PR review, refactoring, naming things, or answering linter/formatter/project-setup questions for Go, TypeScript, JavaScript, Python, HTML, or CSS. Routes to per-language guides covering naming, formatting, tooling, and enforcement.
priority: 5
triggers: [code, code review, code style, style guide, styleguide, refactor, refactoring, naming, rename, linter, lint, formatter, format, golangci, eslint, prettier, ruff, pep8, gofmt, write code, review code, go.md, typescript, javascript, python, html, css, .go, .ts, .tsx, .js, .py]
---

# Code Style Guides — Skill Router

This skill is a collection of opinionated, tooling-aware style guides.
Each guide has two parts: the **rules** (what to write) and the **enforcement**
(how to automate it). Both matter equally.

## When to load which guide

| Situation | Load |
|---|---|
| Writing or reviewing `.go` files | `references/go.md` |
| Writing or reviewing `.ts` / `.tsx` files | `references/typescript.md` |
| Writing or reviewing `.js` / `.mjs` files (no TS) | `references/javascript.md` |
| Writing or reviewing `.py` files | `references/python.md` |
| Writing or reviewing `.html` / `.css` / `.scss` files | `references/html-css.md` |
| Project setup, architecture, naming, commits | `references/general.md` |
| Multi-language task | Load all relevant guides, `references/general.md` always |

## How to apply

1. **Read** the relevant guide(s) first — do not rely on memory.
2. **Apply rules silently** — do not narrate "as per the Go style guide...".
   Just write correct code.
3. **Flag violations** when reviewing existing code. Reference the specific
   section, e.g. "Error should be wrapped: `fmt.Errorf("open config: %w", err)`".
4. **Suggest tooling** when the codebase is missing it (e.g. no `.golangci.yml`,
   no `eslint.config.js`). Paste the minimal config from the relevant guide.

## Quick reference — the non-negotiables across all languages

- Formatter runs on save, mandatory in CI. No manual formatting debates.
- Linter is required — not optional. Config lives in the repo root.
- Errors are handled explicitly. No silent swallows.
- Names communicate intent. Abbreviations only when universally understood.
- Comments explain **why**, not **what**.
