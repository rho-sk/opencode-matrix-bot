BINARY    := opencode-matrix-bot
MODULE    := github.com/rho-sk/opencode-matrix-bot
GO        := $(shell which go 2>/dev/null || echo go)
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT    := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE      := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS   := -s -w \
             -X main.version=$(VERSION) \
             -X main.commit=$(COMMIT) \
             -X main.buildDate=$(DATE)
# goolm = pure-Go olm implementation (no libolm C dependency required)
TAGS      := goolm

DIST_DIR  := dist
PLATFORMS := linux/amd64 linux/arm64

.PHONY: all build test test-verbose cover fmt vet lint check clean dist changelog help

all: build

## build: compile binary for current platform
build:
	$(GO) build -tags "$(TAGS)" -ldflags "$(LDFLAGS)" -o $(BINARY) .

## test: run unit tests (quiet)
test:
	$(GO) test -tags "$(TAGS)" -count=1 ./...

## test-verbose: run unit tests with verbose output
test-verbose:
	$(GO) test -tags "$(TAGS)" -v -count=1 ./...

## cover: run tests and open coverage report
cover:
	$(GO) test -tags "$(TAGS)" -count=1 -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

## fmt: format source code
fmt:
	$(GO) fmt ./...

## vet: run go vet
vet:
	$(GO) vet -tags "$(TAGS)" ./...

## lint: run golangci-lint (must be installed separately)
lint:
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not installed, skipping"; exit 0; }
	golangci-lint run ./...

## check: fmt + vet + test — run before every commit
check: fmt vet test

## dist: build release tarballs for all platforms into dist/
dist: clean-dist
	@mkdir -p $(DIST_DIR)
	@for platform in $(PLATFORMS); do \
		GOOS=$$(echo $$platform | cut -d/ -f1); \
		GOARCH=$$(echo $$platform | cut -d/ -f2); \
		outdir=$(DIST_DIR)/$(BINARY)_$(VERSION)_$${GOOS}_$${GOARCH}; \
		mkdir -p $$outdir; \
		echo "Building $$GOOS/$$GOARCH..."; \
		GOOS=$$GOOS GOARCH=$$GOARCH $(GO) build -tags "$(TAGS)" -ldflags "$(LDFLAGS)" -o $$outdir/$(BINARY) . ; \
		cp deploy/$(BINARY).service $$outdir/ ; \
		cp .env.example $$outdir/ ; \
		cp docs/installation.md $$outdir/ ; \
		cp CHANGELOG.md $$outdir/ 2>/dev/null || true ; \
		tar -czf $(DIST_DIR)/$(BINARY)_$(VERSION)_$${GOOS}_$${GOARCH}.tar.gz -C $(DIST_DIR) $(BINARY)_$(VERSION)_$${GOOS}_$${GOARCH}; \
		rm -rf $$outdir; \
	done
	@echo "Release artifacts in $(DIST_DIR)/"
	@ls -lh $(DIST_DIR)/

## changelog: regenerate CHANGELOG.md from git history (requires git-cliff)
changelog:
	@command -v git-cliff >/dev/null 2>&1 || { echo "git-cliff not installed: https://git-cliff.org/docs/installation"; exit 1; }
	git-cliff --output CHANGELOG.md

## changelog-unreleased: prepend unreleased commits to CHANGELOG.md
changelog-unreleased:
	@command -v git-cliff >/dev/null 2>&1 || { echo "git-cliff not installed: https://git-cliff.org/docs/installation"; exit 1; }
	git-cliff --unreleased --prepend CHANGELOG.md

## clean: remove binary
clean:
	rm -f $(BINARY)
	rm -f coverage.out coverage.html

## clean-dist: remove dist directory
clean-dist:
	rm -rf $(DIST_DIR)

## help: show this help
help:
	@grep -E '^## ' Makefile | sed 's/## //' | column -t -s ':'
