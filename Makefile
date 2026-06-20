SHELL := /bin/bash

.PHONY: help run test e2e mock widget-install widget-dev widget-build core core-run chat doctor vault-list sync-cursor-schema

GO_TAGS ?= webkit2_41
WIDGET_DIR := cmd/sapaloq-widget
MOCK_DIR := cmd/sapaloq-mock
CORE_FLAGS ?=
CORE := go run ./cmd/sapaloq-core $(CORE_FLAGS)
CHAT_MSG ?= halo

help:
	@echo "SapaLOQ — common targets"
	@echo "  make run              — run core + widget dev together (Ctrl+C cleanly stops all child processes)"
	@echo "  make test             — go test ./..."
	@echo "  make e2e              — mock e2e (test/e2e)"
	@echo "  make e2e-live         — live api2 e2e (needs Cursor creds, SAPALOQ_LIVE_E2E=1)"
	@echo "  make core             — sapaloq-core run (CORE_FLAGS='--debug')"
	@echo "  make chat             — one-shot chat (CHAT_MSG=...)"
	@echo "  make doctor           — sapaloq-core doctor"
	@echo "  make vault-list       — sapaloq-core vault list"
	@echo "  make mock             — run mock IPC server (dev)"
	@echo "  make widget-install   — npm install in widget frontend"
	@echo "  make widget-dev       — wails dev (Ubuntu: -tags $(GO_TAGS))"
	@echo "  make widget-build     — wails production build"
	@echo "  make sync-cursor-schema — sync cursor-bridge schema embed"

run:
	@set -euo pipefail; \
	core_pid=""; widget_pid=""; core_pgid=""; widget_pgid=""; \
	cleanup() { \
		trap - INT TERM EXIT; \
		echo ""; \
		echo "Shutting down SapaLOQ..."; \
		for pgid in "$$widget_pgid" "$$core_pgid"; do \
			if [[ -n "$$pgid" && "$$pgid" != "0" ]]; then \
				kill -TERM -- "-$$pgid" 2>/dev/null || true; \
			fi; \
		done; \
		for pid in "$$widget_pid" "$$core_pid"; do \
			[[ -n "$$pid" ]] && pkill -TERM -P "$$pid" 2>/dev/null || true; \
		done; \
		for _ in 1 2 3 4 5 6 7 8 9 10; do \
			alive=0; \
			for pgid in "$$widget_pgid" "$$core_pgid"; do \
				if [[ -n "$$pgid" && "$$pgid" != "0" ]] && \
					 ps -o pid= -p "$$pgid" >/dev/null 2>&1; then alive=1; fi; \
			done; \
			[[ $$alive -eq 0 ]] && break; \
			sleep 0.3; \
		done; \
		for pgid in "$$widget_pgid" "$$core_pgid"; do \
			if [[ -n "$$pgid" && "$$pgid" != "0" ]] && \
				 ps -o pid= -p "$$pgid" >/dev/null 2>&1; then \
				kill -KILL -- "-$$pgid" 2>/dev/null || true; \
			fi; \
		done; \
		pkill -KILL -f 'go run ./cmd/sapaloq-core'   2>/dev/null || true; \
		pkill -KILL -f 'go run ./cmd/sapaloq-widget' 2>/dev/null || true; \
		pkill -KILL -f 'cmd/sapaloq-core'             2>/dev/null || true; \
		pkill -KILL -f 'cmd/sapaloq-widget'           2>/dev/null || true; \
		pkill -KILL -f 'wails dev'                    2>/dev/null || true; \
		pkill -KILL -f 'node .*/vite'                 2>/dev/null || true; \
		pkill -KILL -f 'esbuild --service'            2>/dev/null || true; \
		for p in "$$widget_pid" "$$core_pid"; do \
			if [[ -n "$$p" ]]; then wait "$$p" 2>/dev/null || true; fi; \
		done; \
		rm -f $$HOME/.config/sapaloq/run/sapaloq.sock 2>/dev/null || true; \
		echo "SapaLOQ stopped."; \
	}; \
	trap cleanup INT TERM EXIT; \
	echo "Starting sapaloq-core..."; \
	setsid bash -c '$(CORE) run' </dev/null >/tmp/sapaloq-core.out 2>&1 & core_pid=$$!; \
	core_pgid=$$(ps -o pgid= -p "$$core_pid" 2>/dev/null | tr -d ' '); \
	echo "Starting sapaloq-widget..."; \
	setsid bash -c 'cd $(WIDGET_DIR) && exec wails dev -tags $(GO_TAGS)' \
			</dev/null >/tmp/sapaloq-widget.out 2>&1 & widget_pid=$$!; \
	widget_pgid=$$(ps -o pgid= -p "$$widget_pid" 2>/dev/null | tr -d ' '); \
	echo "core_pid=$$core_pid core_pgid=$$core_pgid  widget_pid=$$widget_pid widget_pgid=$$widget_pgid"; \
	wait -n "$$core_pid" "$$widget_pid" || true; \
	cleanup

test:
	go test ./...

e2e:
	go test ./test/e2e/... -v -count=1 -timeout 120s

e2e-live:
	SAPALOQ_LIVE_E2E=1 go test ./test/e2e/... -v -count=1 -timeout 5m -run Live

e2e-live-strict:
	SAPALOQ_LIVE_E2E=1 SAPALOQ_LIVE_E2E_STRICT=1 go test ./test/e2e/... -v -count=1 -timeout 5m -run Live

core core-run:
	$(CORE) run

chat:
	$(CORE) chat "$(CHAT_MSG)"

doctor:
	$(CORE) doctor

vault-list:
	$(CORE) vault list --limit 20

mock:
	go run ./$(MOCK_DIR)

sync-cursor-schema:
	cp ../cursor-bridge/schema/cursor-bridge.schema.json embed/cursor-bridge.schema.json
	cp embed/cursor-bridge.schema.json internal/bridges/cursor/cursor-bridge.schema.json

widget-install:
	npm install --prefix $(WIDGET_DIR)/frontend

widget-dev:
	cd $(WIDGET_DIR) && wails dev -tags $(GO_TAGS)

widget-build:
	cd $(WIDGET_DIR) && wails build -tags $(GO_TAGS)
