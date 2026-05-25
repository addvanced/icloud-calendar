.PHONY: build install install-plugin test test-go test-python lint smoke clean

BIN_PATH := $(HOME)/bin/icalendar
PLUGIN_DIR := $(HOME)/.hermes/plugins/icalendar
PYTEST_ADDOPTS := -o addopts=

build:
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o $(BIN_PATH) ./cmd/icalendar

install: build install-plugin
	@echo "✓ icalendar binary installed to $(BIN_PATH) (for direct CLI use)"
	@echo "✓ Hermes plugin files copied to $(PLUGIN_DIR)"
	@echo ""
	@echo "End-user install does not need this Makefile:"
	@echo "  hermes plugins install addvanced/icloud-calendar"
	@echo ""
	@echo "After this developer install:"
	@echo "  hermes plugins enable icalendar"
	@echo "  systemctl --user restart hermes-gateway"

install-plugin:
	mkdir -p $(PLUGIN_DIR)
	cp plugin.yaml __init__.py schemas.py tools.py $(PLUGIN_DIR)/
	cp -r skills $(PLUGIN_DIR)/

test: test-go test-python

test-go:
	go test -race -cover ./...

test-python:
	python -m pytest tests/ -q $(PYTEST_ADDOPTS)

lint:
	gofumpt -l .
	goimports -l .
	staticcheck ./...
	gosec -quiet ./...
	govulncheck ./...

smoke: build
	$(BIN_PATH) --json status

clean:
	rm -f $(BIN_PATH)
	rm -rf $(PLUGIN_DIR)/bin
