# Polaris Agent Makefile

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO      ?= go
LINTER  ?= golangci-lint

.PHONY: all build build-cli build-server test lint run clean docker-up docker-down release-dryrun help

all: lint test build ## Run lint, test, then build

# ── Build ──────────────────────────────────────────────────────────────

build: build-cli build-server ## Build both binaries

build-cli: ## Build the CLI binary
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o polaris ./cmd/polaris

build-server: ## Build the server binary
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o polaris-server ./cmd/server

# ── Test & Lint ────────────────────────────────────────────────────────

test: ## Run tests
	$(GO) test -race ./...

lint: ## Run golangci-lint
	$(LINTER) run ./...

# ── Docker ─────────────────────────────────────────────────────────────

docker-up: ## Start the agent server
	docker compose up -d --build

docker-down: ## Stop the agent server
	docker compose down

docker-logs: ## Tail agent logs
	docker compose logs -f

# ── Release ────────────────────────────────────────────────────────────

release-dryrun: ## Dry-run a goreleaser release (no publish)
	goreleaser release --snapshot --clean

release-build: ## Build all release binaries without publishing
	goreleaser build --snapshot --clean

# ── Utilities ──────────────────────────────────────────────────────────

clean: ## Remove build artifacts
	rm -f polaris polaris-server
	rm -rf dist/

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
