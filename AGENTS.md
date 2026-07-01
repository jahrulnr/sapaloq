# AGENTS.md - guide for AI agents & contributors

> Read this before editing. SapaLOQ is a **single Go binary** (orchestrator + event bus + IPC) plus a **Wails widget** (TypeScript/Vite frontend). Module: `github.com/jahrulnr/sapaloq` (Go 1.25).

---

## Golden rules

1. **Keep the build green.** Every change must leave this green:
   ```bash
   go build ./...
   go vet ./...
   go test ./...
   ```
   Frontend changes additionally:
   ```bash
   npm run build --prefix cmd/sapaloq-widget/frontend   # tsc + vite
   ```
2. **Keep docs in sync** (see the mapping below). A behavior change that isn't
   reflected in docs is an incomplete change.
3. **Follow existing conventions.** Mimic surrounding style; don't introduce a
   new dependency or framework without a clear reason. Single-binary, few-deps
   is a product value (see `docs/LIMITATIONS.md`).
4. **Never put real secrets in `config/config.example.json`** - it's a public
   template copied on first boot.
5. **Contract first, security later.** Make the feature behave according to its
   documented contract and user expectations before adding security hardening.
   Premature restrictions, sandboxing, allowlists, and policy layers add
   complexity and frequently create silent workflow bugs. First build the
   simplest bugless end-to-end path; once it works and is covered by functional
   tests, add security incrementally with regression tests proving the original
   contract still works. Do not invent restrictions that the product contract
   does not require. This rule does not permit real-secret exposure, destructive
   behavior, or bypassing an explicit security requirement from the user/spec.
6. **Tests must prove the contract, not only the happy path.** Every behavior
   change requires tests for the successful flow and all reasonably testable
   edge cases: invalid/empty input, cancellation/timeouts, retries, duplicate
   or out-of-order events, persistence/restart recovery, concurrent access,
   partial failures, and relevant boundary values. If an edge case genuinely
   cannot be automated (for example compositor-specific behavior), document
   why and record the manual verification performed. A single happy-path test
   is not sufficient evidence for a non-trivial change.
7. **Refactor over patch.** SapaLOQ started simple; keep it that way. When a
   feature or bug reveals a wrong contract, a divergent code path, or misplaced
   ownership across layers, **fix the source** — align with the existing correct
   pattern, delete the wrong path, reuse shared pipeline — instead of stacking
   guards, flags, regex strippers, or prompt tweaks downstream. Do not trade one
   visible win for permanent complexity (extra branches, duplicate hygiene,
   multi-layer "defense in depth" on the same symptom). A larger diff that
   removes divergence and restores one contract is preferred over a small diff
   that band-aids symptoms. See `docs/BOUNDARIES.md` for layer ownership;
   fix at the layer that should emit or own the behavior, not where the bug
   merely becomes visible (UI, orchestrator consumer, leak sanitizer on the
   wrong channel). Prompt/rules changes are not a substitute for emit/stream bugs.
8. **Centered prompts.** All orchestrator model-facing prose belongs in
   `internal/prompts/` (`defaults/` for user-editable roles, `internal/` for
   ship-only steering). Do not add prompt string literals in orchestrator Go —
   use `prompts.GetInternal` / `RenderInternal`. Discover keys via
   `sapaloq-core prompts list`.
9. **KISS — map the AI workflow as-is; don't force an ideal transcript.**
   SapaLOQ orchestrates and maps provider/model behavior; it does not rewrite
   stream order to match a imagined turn shape. Other patterns are fine when
   they serve a documented contract — not when they fight what the model or
   bridge actually emits. See [AI design (as-is contracts)](#ai-design-as-is-contracts)
   below.

---

## AI design (as-is contracts)

SapaLOQ sits between **providers** (Cursor, Codex, OpenAI-compatible APIs, …)
and **product** (widget, sub-agents, persistence). Design follows each layer's
real contract — not a single universal message order.

### What the orchestrator owns

| Layer | Owns | Does not own |
|-------|------|----------------|
| Bridge | Wire events, provider framing | `turns.json` row order |
| Persist (write) | Append a row when an event completes | Reorder thinking / assistant / tool |
| Replay (read) | Map stored turns → API messages | Renumber or rewrite history |
| Widget (read) | Render `seq` + JSONL tool cards | Regroup by role to "fix" storage |

**Persist (KISS):** `turns.json` appends in **wire order** as events land
(`internal/core/orchestrator/persist_append.go`, stream handlers in
`conversation.go`). `thinking` is persisted when it arrives — not forced before
or after assistant. **Replay:** `actorTurnsToMessages` in `prompt.go` is the
single mapper that adapts storage to what the **next** `Complete()` call needs
(for example assistant before tool when the API requires it). Do not duplicate
that logic at write time — that pattern invited defer/hold bugs.

Cold UI history follows durable `seq`; see `docs/BOUNDARIES.md`,
`docs/CONTEXT-SOP.md`, `docs/UI-DECISION.md`.

### Provider / model message contracts

Contracts vary by provider and model. Follow the one you are bridging to:

- **OpenAI-compatible** — typical roles: `system`, `user`, `assistant`, `tool`;
  some gateways also use `developer`. Tool results follow the assistant turn
  that invoked them in **API replay**, even if the live stream delivered tool
  deltas first.
- **Cursor / Codex (in-bridge MCP)** — execution and framing live in the bridge;
  orchestrator records `EventToolUpdate` and forwards; see
  `docs/CURSOR_AGENT_CONTRACT.md`, `docs/BRIDGE.md`.

Whether the model sends **tool before narration**, **thinking after text**, or
skips thinking entirely is **model behavior**. Do not enforce
`thinking → content → tool` (or any fixed shape) in persist code — that fights
the stream and creates recurring ordering bugs. Fix semantics in the **replay
mapper** or in the **bridge** when the provider contract requires it; document
product policy (greeting, stop) as small pre-append hooks, not transcript
reordering.

### Provider limitations — document, don't pretend

When a provider does not support a role, message kind, or ordering, **note it**
in bridge/orchestrator docs (especially `docs/BRIDGE.md`, `docs/LIMITATIONS.md`,
provider-specific contracts). Examples: a bridge that cannot carry a native
`thinking` role, caps on tool round-trips, or MCP-only tool paths. SapaLOQ may
map unsupported wire into supported storage roles (`thinking` as show-only turn,
tool results as `role=tool`) — that is explicit mapping, not silent reorder of
what we claim happened.

KISS does **not** mean "never use patterns" — it means **one owner per concern**:
append on event, map on replay, document provider limits instead of stacking
write-path special cases.

---

## Keep docs in sync (REQUIRED)

When you change code in an area below, update the listed doc(s) **in the same
change**, and always update `docs/STATUS.md` (status table row + a short
"this session" bullet). Bump the `Last updated:` line in any doc you touch.

| You changed… | Update these docs |
|--------------|-------------------|
| Vault audit log / rotation (`internal/vault/**`) | `docs/RUNTIME.md` (Vault paths + Rotation), `docs/BRIDGE.md` (undeclared section) |
| Config schema / new config block / migration (`internal/config/**`) | `docs/RUNTIME.md` (migration status), `docs/BLUEPRINT.md` (config-domain table + defaults), `config/config.example.json`, `schema/config.schema.json` |
| Role/system prompts (`internal/prompts/**`, `orchestrator.systemPrompt`) | `docs/PROMPT-BUILDER-SOP.md` |
| Tools / tool surface (`internal/core/orchestrator/tools*.go`, `internal/tooldocs/**`) | `docs/BLUEPRINT.md` (tools/defaults), `docs/ORCHESTRATOR.md` |
| Provider/bridge wire formats (`internal/bridges/**`, parsers) | `docs/BRIDGE.md`, `docs/PROVIDER-BRIDGE.md`, `docs/RE-CURSOR-THINKING-TOOLS.md`, `docs/BOUNDARIES.md` (if ownership crosses layers) |
| Layer boundary / transcript contract / cursor MCP ownership | `docs/BOUNDARIES.md`, `docs/CURSOR_AGENT_CONTRACT.md`, `docs/UI-DECISION.md` |
| Event bus / WAL (`internal/bus/**`) | `docs/EVENT-BUS.md` |
| Orchestrator behavior (spawn, anti-poisoning, clarification, completion) | `docs/ORCHESTRATOR.md`, `docs/CONTEXT-SOP.md` |
| Remote/local nodes (`internal/node/**`) | `docs/NODES.md` |
| Platform/desktop adapters (`internal/platform/**`) | `docs/PLATFORM.md`, `docs/DRIVER.md` |
| Widget UI / IPC (`cmd/sapaloq-widget/**`) | `docs/UI-DECISION.md`, `docs/STATUS.md` (UI detail lives in STATUS, not spec docs) |
| Feedback (`SubmitFeedback`, do-not-repeat) | `docs/FEEDBACK-SOP.md` |
| A genuine product/OS limit with no engineering fix | `docs/LIMITATIONS.md` |

If a change spans several areas, update each relevant doc. If you discover a doc
that already contradicts the code, fix it as part of your change.

`docs/STATUS.md` is the single source of truth for "what's implemented" - keep
its status table and the dated session notes current.

---

## Workflow Expectations

- **Explore before editing.** Read the relevant files (and `package.json`, `go.mod`, `Makefile`, etc.) before making changes. Don't assume project structure, dependencies, or available commands.
- **Plan, then implement.** Present a brief implementation plan before making any non-trivial changes.
- **Verify behavior, not just compilation.** Run the relevant tests after changes. For frontend projects, build the application and perform a browser-based verification when feasible.
- **Don't revert** unrelated changes, and **don't push** to a remote repository unless explicitly requested.
- Before implementing a fix, ask: **does this add permanent state/branches, or
  restore one contract?** If two paths should behave the same (e.g. api2 vs api5
  bridge streaming), unify them — don't add a second suppressor upstream.
- Avoid:
  - **Function sprawl:** Don't split logic into excessive helper functions that hurt readability and make navigation harder.
  - **Method proliferation:** Don't introduce new methods unless they provide meaningful reuse, clarity, or separation of concerns.
  - **Over-decomposition:** Keep related logic together. Don't break simple workflows into many tiny functions without a clear benefit.
  - **Premature abstraction:** Don't create reusable abstractions until there is a demonstrated need for them.

---

## Project map (quick orientation)

```
cmd/sapaloq-core/        # headless binary entrypoint (orchestrator, IPC, vault, doctor)
cmd/sapaloq-widget/      # Wails desktop widget (Go + frontend/ TypeScript+Vite)
  frontend/src/          #   main.ts (chat/stream/markdown), style.css, assets/
internal/core/orchestrator/  # SendChat, tools, sub-agents, prompts wiring, audit
internal/prompts/        # defaults/ (user-editable roles) + internal/ (ship-only steering registry)
internal/config/         # config load + schema migration
internal/vault/          # tool-call audit log (+ size rotation/retention)
internal/bus/            # in-process event bus + WAL
internal/bridges/        # cursor-bridge + provider bridges, wire parsers
internal/platform/       # desktop/OS adapters (GNOME-first)
internal/node/           # local/remote sub-agent nodes
config/                  # config.example.json (template), schema/ json schema
docs/                    # design specs + STATUS.md (see doc-sync table above)
migrations/              # Legacy SQL DDL (archived; runtime uses JSON store)
```

## Common commands

```bash
make test            # go test -short ./... + widget frontend vitest (subprocess e2e: make e2e)
make e2e             # e2e suite
make run             # run core
make doctor          # config/infra validation
make widget-build    # build the Wails widget (wails build)
make widget-dev      # widget dev mode
```

> Tip: config defaults to `~/.config/sapaloq/config.json` (override via
> `SAPALOQ_CONFIG`). Non-config runtime data defaults to `~/SapaLOQ/`:
> `prompts/`, `skills/`, `workspace/`, `memory/`, `state/`, `vault/`, `run/`.
