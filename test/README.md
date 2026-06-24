# test/

Integration and end-to-end tests will live here (future).

## Live simulate suite (real LLM, mocked sub-agents)

`internal/core/orchestrator/simulate_live_test.go` drives the orchestrator /
planner / agent loop against a real OpenAI-compatible provider (Blackbox) in one
role at a time, mocking the other roles and tooling. The tests `t.Skip` unless
opted in, so the default `go test ./...` stays offline-green.

Run them live via the Makefile (recommended):

```bash
export BLACKBOX_API_KEY=sk-...   # token comes from the environment, never the Makefile
make simulate-live               # all three modes

# one mode:
make simulate-live SIMULATE_RUN=TestSimulateOrchestratorPlannerAgentRoundTrip

# override the model/endpoint:
make simulate-live BLACKBOX_MODEL=blackboxai/minimax/minimax-m2.7
```

Or directly with `go test`:

```bash
SAPALOQ_BLACKBOX_E2E=1 \
  BLACKBOX_API_KEY=sk-... \
  BLACKBOX_MODEL=blackboxai/anthropic/claude-sonnet-4.5 \
  BLACKBOX_ENDPOINT=https://api.blackbox.ai/v1 \
  go test ./internal/core/orchestrator -run TestSimulate -v
```

- `BLACKBOX_ENDPOINT` may be a bare base URL (`…/v1`); it is auto-completed to
  `/chat/completions`.
- Override the token env var name with `BLACKBOX_CREDENTIALS_ENV` (default
  `BLACKBOX_API_KEY`).
- Mode 1 is the live regression for the ask.md spawn-before-acknowledge fix.
