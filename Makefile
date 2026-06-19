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
	@echo "  make run              — run core + widget dev together (Ctrl+C stops both)"
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
	core_pid=""; widget_pid=""; \
	cleanup() { \
		trap - INT TERM EXIT; \
		echo ""; \
		echo "Shutting down SapaLOQ..."; \
		if [[ -n "$$widget_pid" ]] && kill -0 "$$widget_pid" 2>/dev/null; then pkill -TERM -P "$$widget_pid" 2>/dev/null || true; kill -TERM "$$widget_pid" 2>/dev/null || true; fi; \
		if [[ -n "$$core_pid" ]] && kill -0 "$$core_pid" 2>/dev/null; then pkill -TERM -P "$$core_pid" 2>/dev/null || true; kill -TERM "$$core_pid" 2>/dev/null || true; fi; \
		wait "$$widget_pid" 2>/dev/null || true; \
		wait "$$core_pid" 2>/dev/null || true; \
	}; \
	trap cleanup INT TERM EXIT; \
	echo "Starting sapaloq-core..."; \
	$(CORE) run & core_pid=$$!; \
	echo "Starting sapaloq-widget..."; \
	( cd $(WIDGET_DIR) && wails dev -tags $(GO_TAGS) ) & widget_pid=$$!; \
	wait -n "$$core_pid" "$$widget_pid"

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
