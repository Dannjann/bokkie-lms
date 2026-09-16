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

.PHONY: all setup dev web-dev web-install web-build install-local test vet-all build-all keygen stage-scripts sign release clean ci help

all: build-all

help:
	@echo "setup       install the pre-push hook (run once after clone)"
	@echo "dev         run the server against the Vite dev server (run web-dev too)"
	@echo "web-dev     run the Vite dev server with hot reload"
	@echo "web-build   build the SPA into web/dist"
	@echo "install-local  build this tree onto this machine's service (ARGS='--dry-run')"
	@echo "test        go test ./..."
	@echo "vet-all     go vet once per GOOS, so build-tagged files are type-checked"
	@echo "build-all   five binaries into dist/, embedding whatever web/dist holds"
	@echo "keygen      create the release signing key (once, ever)"
	@echo "sign        write and sign dist/latest.json for the current tag"
	@echo "release     web-build + build-all + sign — the folder that goes on the USB stick"
	@echo "ci          vet-all + test + build-all — the gate"
	@echo "clean       remove dist/ and the generated web/dist stub"

setup:
	git config core.hooksPath .githooks
	@echo "pre-push hook active: 'make ci' runs before every push (--no-verify to skip)"

# Development is two processes: Vite serving the frontend with hot reload, and
# bokkie proxying to it. Both are reached on bokkie's port, so there is one
# origin in development and in production. Run `make web-dev` in another
# terminal.
dev:
	go run ./cmd/bokkie run -dev

web-dev: web-install
	cd web && npm run dev

web-install:
	cd web && npm install

# The real SPA, which build-all then embeds. Kept separate from build-all so a
# Go-only change does not need node installed.
web-build: web-install
	cd web && npm run build

# Put this working tree on this machine's service, so a change can be seen where
# it will actually run rather than only on the Vite dev server.
#
# Not a release: it builds from source and skips the signature check, which is
# the developer's trade and nobody else's. The station's path is a signed folder
# from `make release` and the double-click wrappers inside it.
install-local:
	@sh scripts/dev-install.sh $(ARGS)

test: web/dist
	go test ./...

# go vet only type-checks the files selected by the host's build tags. Without
# looping over GOOS, a broken spooler_windows.go sits undetected until someone
# builds for Windows.
vet-all: web/dist
	@for os in $(GOOSES); do \
		echo "go vet GOOS=$$os"; \
		GOOS=$$os go vet ./... || exit 1; \
	done

# go:embed refuses to compile against a directory that does not exist, so every
# target that compiles Go code depends on this stub. The real contents come from
# `make web-build`; this keeps a clean checkout buildable without node installed
# and without committing anything build-output-shaped to git.
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

# The release key. This is run once, ever: every host already installed trusts
# the public half, so a second key pair would lock all of them out of updates.
# The private half never enters this repository.
keygen:
	go run ./cmd/bokkie-release keygen

# latest.json plus its signature, describing every binary in dist/. One
# signature covers the whole release because the manifest carries each binary's
# SHA-256 — altering a binary fails its hash, altering the hash fails the
# signature.
sign:
	go run ./cmd/bokkie-release sign dist

# The double-click wrappers travel with the binaries. Without them a release
# folder is five executables and a JSON file, and the person it is handed to has
# to know which one to run and how to run it with administrator rights.
#
# Named one by one rather than `scripts/*`: scripts/ also holds developer
# tooling that builds from source and skips the signature check, and a glob here
# is how that would end up on a USB stick bound for the station.
stage-scripts: | dist
	cp scripts/*.cmd scripts/*.command scripts/pick-binary.sh dist/
	@chmod +x dist/*.command dist/pick-binary.sh

dist:
	@mkdir -p dist

# A real release, and deliberately stricter than build-all: it refuses to run
# without an exact version tag and without the signing key, because an unsigned
# or untagged build is precisely what must never reach the station.
release: web-build build-all stage-scripts sign

ci: vet-all test build-all
	@echo "ci: ok"

clean:
	rm -rf dist web/dist
