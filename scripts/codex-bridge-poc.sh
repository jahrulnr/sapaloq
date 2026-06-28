#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

echo "==> probing real Codex app-server lifecycle over a Unix WebSocket"
go test -tags=e2e ./internal/bridges/codex/... \
  -run TestE2EAppServerLifecycle -count=1 -v

if [[ "${1:-}" == "--turn" ]]; then
  echo "==> running one authenticated model turn"
  SAPALOQ_CODEX_E2E=1 go test -tags=e2e ./internal/bridges/codex \
    -run TestE2EAppServerTurn -count=1 -v
else
  echo "Pass --turn to include a live model request."
fi
