SHELL := /bin/bash

.PHONY: help run test e2e mock widget-install widget-dev widget-build core core-run chat doctor vault-list sync-cursor-schema install uninstall

GO_TAGS ?= webkit2_41
WIDGET_DIR := cmd/sapaloq-widget
MOCK_DIR := cmd/sapaloq-mock
CORE_FLAGS ?=
CORE := go run ./cmd/sapaloq-core $(CORE_FLAGS)
CHAT_MSG ?= halo

# --- local install (build from source) -----------------------------------
# `make install` builds the binaries from this checkout and installs them into
# BIN_DIR, seeds a default config (never overwriting an existing one) and — by
# default — registers the systemd --user service. This is the DEVELOPMENT path;
# end users should use install.sh (downloads a prebuilt release).
BIN_DIR ?= $(HOME)/.local/bin
DATA_DIR ?= $(if $(XDG_CONFIG_HOME),$(XDG_CONFIG_HOME),$(HOME)/.config)/sapaloq
INSTALL_SERVICE ?= 1
INSTALL_AUTOSTART ?= 1
CORE_BIN := sapaloq-core
WIDGET_BIN := sapaloq-widget
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

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
	@echo "  make install          — build from source + install to BIN_DIR ($(BIN_DIR)) + systemd service"
	@echo "                          vars: BIN_DIR, GO_TAGS, INSTALL_SERVICE=0, INSTALL_AUTOSTART=0"
	@echo "  make uninstall        — remove service + binaries (config/data kept)"

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

install:
	@set -euo pipefail; \
	echo "==> Installing SapaLOQ ($(VERSION)) from source to $(BIN_DIR)"; \
	mkdir -p "$(BIN_DIR)"; \
	echo "==> Building $(CORE_BIN)"; \
	go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o "$(BIN_DIR)/$(CORE_BIN)" ./cmd/sapaloq-core; \
	echo "    installed $(BIN_DIR)/$(CORE_BIN)"; \
	if command -v wails >/dev/null 2>&1; then \
		echo "==> Building $(WIDGET_BIN) (wails)"; \
		if ( cd "$(WIDGET_DIR)" && wails build -tags "$(GO_TAGS)" ); then \
			out="$(WIDGET_DIR)/build/bin/$(WIDGET_BIN)"; \
			if [[ -f "$$out" ]]; then \
				install -m 0755 "$$out" "$(BIN_DIR)/$(WIDGET_BIN)"; \
				echo "    installed $(BIN_DIR)/$(WIDGET_BIN)"; \
			else \
				echo "[warn] wails build finished but $$out missing; skipping widget" >&2; \
			fi; \
		else \
			echo "[warn] wails build failed; skipping widget (core is installed)" >&2; \
		fi; \
	else \
		echo "[warn] wails not found; skipping GUI widget. Install: go install github.com/wailsapp/wails/v2/cmd/wails@latest" >&2; \
	fi; \
	mkdir -p "$(DATA_DIR)" "$(DATA_DIR)/memory" "$(DATA_DIR)/state" "$(DATA_DIR)/run" "$(DATA_DIR)/vault"; \
	cfg="$(DATA_DIR)/config.json"; \
	if [[ -f "$$cfg" ]]; then \
		echo "    config exists, leaving it untouched: $$cfg"; \
	elif [[ -f config/config.example.json ]]; then \
		cp config/config.example.json "$$cfg"; \
		echo "    seeded default config: $$cfg"; \
	else \
		echo "[warn] no config/config.example.json; run 'sapaloq-core doctor'" >&2; \
	fi; \
	case ":$$PATH:" in *":$(BIN_DIR):"*) : ;; *) echo "[warn] $(BIN_DIR) is not on PATH; add: export PATH=\"$(BIN_DIR):\$$PATH\"" >&2 ;; esac; \
	if [[ "$(INSTALL_SERVICE)" == "1" ]]; then \
		if command -v systemctl >/dev/null 2>&1; then \
			echo "==> Registering systemd --user service"; \
			if [[ "$(INSTALL_AUTOSTART)" == "0" ]]; then \
				SAPALOQ_SKIP_WIDGET_AUTOSTART=1 "$(BIN_DIR)/$(CORE_BIN)" service install; \
			else \
				"$(BIN_DIR)/$(CORE_BIN)" service install; \
			fi; \
		else \
			echo "[warn] systemctl not found; skipping service install. Run: $(CORE_BIN) run" >&2; \
		fi; \
	else \
		echo "==> Skipping service install (INSTALL_SERVICE=0). Later: $(CORE_BIN) service install"; \
	fi; \
	echo "==> SapaLOQ installed."

uninstall:
	@set -euo pipefail; \
	echo "==> Uninstalling SapaLOQ"; \
	if [[ -x "$(BIN_DIR)/$(CORE_BIN)" ]]; then \
		"$(BIN_DIR)/$(CORE_BIN)" service uninstall || echo "[warn] service uninstall reported an issue (continuing)" >&2; \
	else \
		echo "[warn] $(CORE_BIN) not found in $(BIN_DIR); skipping service uninstall" >&2; \
	fi; \
	for bin in "$(CORE_BIN)" "$(WIDGET_BIN)"; do \
		if [[ -e "$(BIN_DIR)/$$bin" ]]; then rm -f "$(BIN_DIR)/$$bin"; echo "    removed $(BIN_DIR)/$$bin"; fi; \
	done; \
	echo "==> Done. Config and data kept at: $(DATA_DIR)"
