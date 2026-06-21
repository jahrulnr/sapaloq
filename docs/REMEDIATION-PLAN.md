# SapaLOQ — Architecture Remediation Plan

> Recovery plan after a full documentation-to-code audit.
> Last updated: 2026-06-21

## Objective

Restore one trustworthy contract across docs, config, runtime behavior, and
tests without removing intentional capabilities such as `system_exec` in Ask
and Plan for exploration.

The governing rule is:

> A feature is not implemented merely because it exists in Markdown, JSON
> schema, example config, or an LLM prompt. Runtime code must consume and
> enforce it, and a behavioral test must prove it.

## Root causes

1. **Configuration has three conflicting truths.**
   `config.example.json`, `schema/config.schema.json`, and Go structs use
   different names and support different blocks. Unknown JSON is silently
   ignored by `encoding/json`, so `/settings` can report success for a no-op.
2. **Policy is sometimes prompt-only.**
   Tool profiles are declared, but shared tool dispatch currently happens
   before the sub-agent role gate. An undeclared/provider-poisoned call can
   therefore bypass a role allowlist.
3. **Roadmap configuration is presented as live configuration.**
   Task stack, anti-poisoning, spawn routing, concurrency, lifecycle control,
   context ingress, and learning knobs exist in docs/schema/example but are
   mostly not consumed by Go.
4. **Partial infrastructure is marked too broadly as complete.**
   Event WAL replay counts records but does not rehydrate consumers; remote
   node transport exists but is not in the execution path; platform detection
   does not implement the documented `os.json` cache.
5. **Tests mostly prove local functions, not architectural invariants.**
   The suite is green while config changes can be ignored and role policy can
   be bypassed.

## Design decisions preserved

- Ask and Plan may use `system_exec` for exploration.
- Plan remains read-only with respect to target artifacts; terminal inspection
  is not considered implementation.
- Agent remains the mutating executor.
- Scribe may only mutate through `scribe_write_note`.
- Single Go core binary, in-process bus, SQLite, and no mandatory external
  broker remain fixed.
- Companion and coding-worker memory remain isolated.

## Remediation sequence

### P0 — Restore contract truth and enforcement

1. Add contract tests covering example-config ↔ schema ↔ Go field parity.
2. Align active config names for platform, vault, skills, prompts, and event
   WAL. Remove active-example keys that the runtime does not consume.
3. Make `/settings` reject unsupported/no-op or restart-only paths instead of
   claiming they were hot-reloaded.
4. Apply the sub-agent role gate before every shared tool dispatch. Preserve
   `system_exec` for Ask/Plan; deny it to roles whose allowlist excludes it.
5. Validate tool profiles at startup so typos cannot silently widen or disable
   a role.
6. Correct `docs/STATUS.md` claims and stale roadmap rows.

Acceptance:

- The public example validates against the public schema.
- Every key in the public example is either consumed by Go or explicitly
  informational and tested as such.
- A poisoned `system_exec` call from Scribe is denied; the same call from
  Planner succeeds.
- Unsupported `/settings` patches return an error.
- Build, vet, unit, integration, E2E, and frontend build pass.

### P1 — Make orchestration state real

1. Replace “latest task directory” heuristics with a persisted task registry:
   active, parked, done, session, mode, parent plan, and timestamps.
2. Consume `maxConcurrentSubAgents`; queue scheduled work at capacity.
3. Enforce anti-context-poisoning task switching in code.
4. Make Plan → Agent handoff task-scoped. Never attach an unrelated “latest
   plan”; implement the configured review/approval policy.
5. Implement lifecycle state transitions and control acknowledgements, or
   remove unsupported controls from active config until implemented.

Acceptance:

- A second unrelated task cannot silently blend into the active task.
- Capacity limits are deterministic and covered under race tests.
- Agent execution references exactly one approved plan or explicitly runs
  direct without a plan.
- Restart restores task state without directory-mtime guessing.

### P2 — Complete event and recovery semantics

1. Define durable event consumer checkpoints.
2. Replay WAL events into registered consumers, not only count them at boot.
3. Add socket `publish` with topic validation and producer identity.
4. Implement stale-task watchdog and terminal-event enforcement.
5. Close the bus cleanly during shutdown and expose dropped-event counters.

Acceptance:

- A completion event published before a simulated restart is consumed once
  after restart.
- Slow subscribers cannot block publishers, and drops are observable.
- External producers can publish through the same socket contract.

### P3 — Implement the Context SOP as runtime, not prose

1. Add typed context config and a deterministic ingress result.
2. Implement intent classification, namespace/mode resolution, FTS prefetch,
   bounded skill selection, and a context packet.
3. Enforce anti-deep-check budgets while retaining explicit Ask/Plan
   exploration capability.
4. Build prompt assembly from role + task + prefetch + feedback; keep the
   orchestrator transcript bounded.
5. Add prefetch telemetry and compaction reload from the index.

Acceptance:

- High-confidence indexed tasks avoid redundant exploration.
- Low-confidence tasks retain bounded exploration and clarification.
- Compaction resumes from durable task/index state, not a lossy transcript
  summary alone.

### P4 — Finish or honestly defer nodes, platform, and learning

1. Wire remote node envelopes and progress/control into real execution, or mark
   remote execution unavailable in config and status.
2. Implement the `os.json` fingerprint/cache contract, or retire that contract
   in favor of the current direct detector.
3. Add learning queue, janitor, overlays, and research only after context
   ingress is stable.
4. Keep deferred features out of first-boot config until they have runtime
   consumers and behavioral tests.

## Working method

Each phase follows the same gate:

1. Write or update an invariant test that fails.
2. Implement the smallest complete runtime behavior.
3. Run focused tests.
4. Update the mapped modular docs and `docs/STATUS.md`.
5. Run the full quality gates.

No subsystem advances to the next phase while its config can still succeed as
a silent no-op.
