# bokkie — build and development targets.
#
# `make ci` is the gate. There is no hosted CI by design, so the pre-push hook
# installed by `make setup` is what actually makes it run. See docs/plans/01-foundation.md.

MODULE  := github.com/Dannjann/bokkie-lms
PKG     := $(MODULE)/internal/buildinfo
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(PKG).Version=$(VERSION) \
	-X $(PKG).Commit=$(COMMIT) \
	-X $(PKG).Date=$(DATE)

# Load-bearing, not a preference: a cgo dependency (almost certainly a SQLite
# driver) ends cross-compilation for every non-host target at once, and with it
# the host-OS freedom in Decision 5. See docs/plans/00-architecture.md.
export CGO_ENABLED := 0

GOOSES := windows darwin linux

.PHONY: all setup dev test vet-all build-all clean ci help

all: build-all

help:
	@echo "setup      install the pre-push hook (run once after clone)"
	@echo "dev        run the server in the foreground"
	@echo "test       go test ./..."
	@echo "vet-all    go vet once per GOOS, so build-tagged files are type-checked"
	@echo "build-all  five release binaries into dist/"
	@echo "ci         vet-all + test + build-all — the gate"
	@echo "clean      remove dist/ and the generated web/dist stub"

setup:
	git config core.hooksPath .githooks
	@echo "pre-push hook active: 'make ci' runs before every push (--no-verify to skip)"

# Filled in properly at task 1.6, when the Vite dev server exists to proxy to.
dev:
	go run ./cmd/bokkie run

test:
	go test ./...

# go vet only type-checks the files selected by the host's build tags. Without
# looping over GOOS, a broken spooler_windows.go sits undetected until someone
# builds for Windows.
vet-all:
	@for os in $(GOOSES); do \
		echo "go vet GOOS=$$os"; \
		GOOS=$$os go vet ./... || exit 1; \
	done

# go:embed refuses to compile against a directory that does not exist. The real
# contents come from Vite at task 1.6; this keeps a clean checkout buildable
# without committing anything build-output-shaped to git.
web/dist:
	@mkdir -p web/dist
	@echo '<!doctype html><title>bokkie</title><p>SPA not built — run the Vite build (task 1.6).' > web/dist/index.html

build-all: web/dist
	@mkdir -p dist
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/bokkie-win-amd64.exe ./cmd/bokkie
	GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/bokkie-mac-arm64    ./cmd/bokkie
	GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/bokkie-mac-amd64    ./cmd/bokkie
	GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/bokkie-linux-amd64  ./cmd/bokkie
	GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/bokkie-linux-arm64  ./cmd/bokkie
	@echo "--- dist ---" && ls -1 dist/

ci: vet-all test build-all
	@echo "ci: ok"

clean:
	rm -rf dist web/dist
