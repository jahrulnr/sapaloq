# General Code Style Principles

Applies across all languages and frameworks. These are concrete rules, not
just philosophy.

---

## 1. Core Philosophy

- **Readable first.** Code is read 10× more than it is written. Optimize for
  the reader, not the writer.
- **Consistent over clever.** Follow existing patterns even if you disagree.
  Consistency is worth more than local correctness.
- **Simple over complex.** The simplest solution that correctly solves the
  problem wins. Complexity must be justified, not defaulted into.
- **Comments explain why, not what.** If the code needs a comment to explain
  what it does, consider renaming or restructuring first.

---

## 2. Naming

- **Be specific.** `userID` not `id`; `parseConfigFile` not `parse`.
- **Avoid abbreviations** unless universally understood (`ctx`, `err`, `db`,
  `id`, `url` are fine; `tmr`, `cfg2`, `hlpr` are not).
- **Boolean names** start with `is`, `has`, `can`, `should`. Never a noun.
  - ✅ `isActive`, `hasPermission`
  - ❌ `active`, `permission`
- **Functions are verbs.** `fetchUser`, `validateToken`, `sendNotification`.
- **Avoid filler words.** `Manager`, `Handler`, `Helper`, `Util` in type names
  are usually a sign the abstraction is unclear.
- **No magic numbers.** Every numeric literal that isn't 0 or 1 needs a named
  constant.

---

## 3. File & Folder Structure

- **One primary concern per file.** A file that does three unrelated things
  should be three files.
- **File names are lowercase.** Use underscores (`user_service.go`) or hyphens
  (`user-service.ts`) depending on language convention. Never PascalCase for
  filenames.
- **Group by feature, not by type.** Prefer `features/auth/` over `handlers/`,
  `models/`, `services/` at the top level. Exception: shared utilities.
- **Keep nesting shallow.** Three levels deep is usually enough.
  `src/features/auth/components/` — good.
  `src/a/b/c/d/e/f/` — refactor.

---

## 4. Functions & Methods

- **Short functions win.** If a function doesn't fit on one screen (~50 lines),
  consider splitting it.
- **Single responsibility.** A function should do one thing. If you're writing
  "and" in the docstring, it's doing two things.
- **No side effects in getters.** A function named `get*` or `fetch*` should
  not mutate state.
- **Avoid deep nesting.** Return early / use guard clauses instead of deeply
  nested `if/else` blocks.
  ```
  // ❌
  if user != nil {
      if user.IsActive {
          if user.HasPermission("read") {
              // actual logic
          }
      }
  }

  // ✅
  if user == nil { return ErrNoUser }
  if !user.IsActive { return ErrInactiveUser }
  if !user.HasPermission("read") { return ErrForbidden }
  // actual logic
  ```

---

## 5. Error Handling

- **Never silently ignore errors.** Every error must either be handled or
  explicitly propagated.
- **Log at the boundary, not at every layer.** Log once when an error reaches
  the top of the call stack. Avoid logging the same error five times across
  five layers.
- **Fail fast.** Validate inputs at the entry point. Don't let bad data
  propagate deep into business logic.

---

## 6. Comments & Documentation

- **Document public APIs.** Every exported function, type, or constant needs
  a doc comment.
- **TODO format:** `TODO(username): description` — always include who owns it.
- **Remove dead code.** Don't comment out old code and commit it. That's what
  git history is for.
- **Don't comment the obvious:**
  ```
  // ❌ Increment i by 1
  i++

  // ✅ (no comment needed)
  i++

  // ✅ Retry limit is empirically derived from p99 timeout of downstream service
  const maxRetries = 3
  ```

---

## 7. Commits & Version Control

- **Conventional Commits** format — enforced via `commitlint` in CI:
  ```
  <type>(<scope>): <description>

  feat(auth): add JWT refresh token rotation
  fix(api): handle nil pointer in user lookup
  refactor(db): extract connection pool config
  docs(readme): add local dev setup instructions
  chore(ci): upgrade golangci-lint to v1.59
  ```
- **Types:** `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `perf`, `ci`
- **Atomic commits.** One logical change per commit. Don't bundle a refactor
  with a bug fix.
- **No "fix" commits that are just formatting.** Run the formatter before
  committing, not after.

---

## 8. Tooling Baseline (all projects)

Every repository must have these at the root, regardless of language:

| File | Purpose |
|---|---|
| `.editorconfig` | Cross-editor formatting baseline |
| `.gitignore` | Tracked via template for the language |
| `README.md` | Setup, run, test, deploy — in that order |
| CI config | Lint + test on every PR, no exceptions |

Minimal `.editorconfig`:
```ini
root = true

[*]
indent_style = space
indent_size = 2
end_of_line = lf
charset = utf-8
trim_trailing_whitespace = true
insert_final_newline = true

[*.go]
indent_style = tab

[*.py]
indent_size = 4

[Makefile]
indent_style = tab
```

---

## 9. Code Review Checklist

Before requesting a review, verify:
- [ ] All new code has tests
- [ ] Formatter has been run
- [ ] Linter passes with zero new warnings
- [ ] No debug/dead code committed
- [ ] Public APIs are documented
- [ ] Error cases are handled
- [ ] No secrets or credentials in the diff
