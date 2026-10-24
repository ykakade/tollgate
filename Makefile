# Tollgate build entry point. Run "make help" to see the targets.

GO      ?= go
BIN     ?= bin
SDK_DIR := test/sdk

GOVULNCHECK_VERSION ?= v1.9.0

# The race detector needs cgo and a C compiler. If the system has no C
# compiler, the tests run without the race detector and make shows a warning.
# CI always has a C compiler. Set RACE= to turn the race detector off.
CC   ?= cc
RACE ?= $(if $(shell command -v $(CC) 2>/dev/null),-race,)

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show the targets.
	@awk 'BEGIN {FS = ":.*## "} /^[a-z-]+:.*## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build the tollgate and fakeprovider binaries into bin/.
	CGO_ENABLED=0 $(GO) build -trimpath -o $(BIN)/ ./cmd/...

.PHONY: test
test: ## Run the unit tests with the race detector and show coverage.
ifeq ($(RACE),)
	@echo "warning: no C compiler found, so the tests run without the race detector" >&2
endif
	CGO_ENABLED=$(if $(RACE),1,0) $(GO) test $(RACE) -coverprofile=coverage.out ./...

.PHONY: test-integration
test-integration: ## Run the SDK tests against the fake provider.
	CGO_ENABLED=$(if $(RACE),1,0) $(GO) -C $(SDK_DIR) test $(RACE) ./...

.PHONY: golden
golden: ## Write the golden files again from the current output.
	$(GO) test ./internal/fakeprovider -update

.PHONY: lint
lint: ## Run golangci-lint on all modules.
	golangci-lint run ./...
	cd $(SDK_DIR) && golangci-lint run --config ../../.golangci.yml ./...

.PHONY: fmt-check
fmt-check: ## Fail if a Go file is not formatted.
	@out="$$(gofmt -l . )"; if [ -n "$$out" ]; then echo "These files are not formatted. Run gofmt -w on them:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## Run go vet on all modules.
	$(GO) vet ./...
	$(GO) -C $(SDK_DIR) vet ./...

.PHONY: tidy-check
tidy-check: ## Fail if go.mod or go.sum is not tidy.
	$(GO) mod tidy -diff
	$(GO) -C $(SDK_DIR) mod tidy -diff

.PHONY: vuln
vuln: ## Scan for known vulnerabilities with govulncheck.
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...
	cd $(SDK_DIR) && $(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

.PHONY: links
links: ## Check the links in the Markdown files. Needs lychee.
	@command -v lychee >/dev/null || { echo "lychee is not installed. Install it from https://lychee.cli.rs and try again." >&2; exit 1; }
	lychee --no-progress './**/*.md'

.PHONY: check
check: fmt-check vet tidy-check build test test-integration lint ## Run all local checks that CI runs, except vuln and links.

.PHONY: clean
clean: ## Remove build output.
	rm -rf $(BIN) coverage.out
