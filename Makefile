# TRK local development. `make` or `make help` lists targets.
#
# Dev targets use their own port and data dir, so they never touch a real
# trk on :7777 or your real ~/.claude config.

GO        ?= $(shell command -v go 2>/dev/null || echo $(HOME)/.local/go/bin/go)
BIN       := bin/trk
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X github.com/GujaLomsadze/trk/internal/version.Version=$(VERSION)

DEV_PORT  ?= 17777
DEV_DATA  ?= /tmp/trk-dev
DEV_CLAUDE ?= /tmp/trk-claude-sandbox
DEV_ENV   := TRK_PORT=$(DEV_PORT) TRK_DATA_DIR=$(DEV_DATA) TRK_URL=
DEV_URL   := http://localhost:$(DEV_PORT)

INSTALL_DIR ?= $(HOME)/.local/bin

.DEFAULT_GOAL := help

.PHONY: help build run dev demo seed stop status logs test race e2e check fmt vet \
        cross snapshot init-dry init-sandbox install clean

help: ## List targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  \033[36m%-13s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo
	@echo "  dev daemon: $(DEV_URL)   data: $(DEV_DATA)"

## ---- build & run -------------------------------------------------------

build: ## Build ./bin/trk
	@mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/trk

run: build ## Run the dev daemon in the foreground (Ctrl+C to stop)
	$(DEV_ENV) $(BIN) serve

dev: demo ## Alias for demo

demo: build stop ## Start the dev daemon in the background, seed a fake fleet, print the URL
	@mkdir -p $(DEV_DATA)
	@$(DEV_ENV) nohup $(BIN) serve >/dev/null 2>&1 &
	@for i in $$(seq 1 30); do curl -fs $(DEV_URL)/healthz >/dev/null && break; sleep 0.1; done
	@TRK_PORT=$(DEV_PORT) sh scripts/seed-demo.sh
	@echo "dashboard: $(DEV_URL)   (make stop to shut down)"

seed: ## Post demo events to the running dev daemon
	TRK_PORT=$(DEV_PORT) sh scripts/seed-demo.sh

stop: ## Stop the dev daemon
	@if [ -f $(DEV_DATA)/trk.pid ]; then kill $$(cat $(DEV_DATA)/trk.pid) 2>/dev/null || true; sleep 0.3; echo "stopped dev daemon"; fi

status: ## Is the dev daemon up?
	@curl -fs $(DEV_URL)/healthz && echo || echo "dev daemon not running"

logs: ## Tail the dev daemon log
	tail -f $(DEV_DATA)/trk.log

## ---- quality -----------------------------------------------------------

test: ## Unit tests
	$(GO) test ./...

race: ## Unit tests with the race detector
	$(GO) test -race -count=1 ./...

e2e: ## End-to-end acceptance test (Linux/macOS)
	$(GO) test -tags e2e -count=1 ./e2e/...

fmt: ## gofmt everything
	gofmt -w cmd internal web e2e

vet: ## go vet (incl. e2e build tag)
	$(GO) vet ./...
	$(GO) vet -tags e2e ./e2e/...

check: vet race e2e ## Everything CI runs
	@test -z "$$(gofmt -l cmd internal web e2e)" || (echo "gofmt needed:"; gofmt -l cmd internal web e2e; exit 1)

## ---- claude integration (safe) -----------------------------------------

init-dry: build ## Show what `trk init` would change in your REAL ~/.claude, write nothing
	$(BIN) init --dry-run

init-sandbox: build ## Run `trk init` against a throwaway Claude config dir and show the result
	@rm -rf $(DEV_CLAUDE) && mkdir -p $(DEV_CLAUDE)
	@[ -f $(HOME)/.claude/settings.json ] && cp $(HOME)/.claude/settings.json $(DEV_CLAUDE)/ || true
	CLAUDE_CONFIG_DIR=$(DEV_CLAUDE) $(DEV_ENV) $(BIN) init --yes
	@echo; echo "--- $(DEV_CLAUDE)/settings.json"; cat $(DEV_CLAUDE)/settings.json

## ---- release -----------------------------------------------------------

cross: ## Build all 6 release targets (no archives)
	@for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do \
		os=$${t%/*}; arch=$${t#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/trk_$${os}_$${arch}$$ext ./cmd/trk && echo "  $$t"; \
	done

snapshot: ## GoReleaser snapshot into ./dist (no publish)
	$(GO) run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean --skip=publish

install: build ## Install ./bin/trk to ~/.local/bin (then: trk init)
	@mkdir -p $(INSTALL_DIR)
	install -m 0755 $(BIN) $(INSTALL_DIR)/trk
	@echo "installed $(INSTALL_DIR)/trk — next: trk init --dry-run, then trk init"

clean: stop ## Remove build output and dev data
	rm -rf bin dist $(DEV_DATA) $(DEV_CLAUDE)
