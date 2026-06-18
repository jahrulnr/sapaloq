.PHONY: help test mock widget-install widget-dev widget-build core

GO_TAGS ?= webkit2_41
WIDGET_DIR := cmd/sapaloq-widget
MOCK_DIR := cmd/sapaloq-mock

help:
	@echo "SapaLOQ — common targets"
	@echo "  make test           — go test ./..."
	@echo "  make mock           — run mock IPC server (dev)"
	@echo "  make widget-install — npm install in widget frontend"
	@echo "  make widget-dev     — wails dev (Ubuntu: -tags $(GO_TAGS))"
	@echo "  make widget-build   — wails production build"
	@echo "  make core           — run sapaloq-core placeholder"

test:
	go test ./...

mock:
	go run ./$(MOCK_DIR)

core:
	go run ./cmd/sapaloq-core

widget-install:
	npm install --prefix $(WIDGET_DIR)/frontend

widget-dev:
	cd $(WIDGET_DIR) && wails dev -tags $(GO_TAGS)

widget-build:
	cd $(WIDGET_DIR) && wails build -tags $(GO_TAGS)
