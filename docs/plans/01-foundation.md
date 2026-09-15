# Step 1 — Build and Runtime Foundation

**Status:** not started · **Depends on:** nothing · **Blocks:** Steps 2–10

---

## Goal

A binary that installs as a service, boots, serves a page, and **proves the
cross-platform claim before any feature can quietly break it.**

No business logic in this step. Not one lending concept. The output is
infrastructure, and the reason it comes first is that every decision here is
expensive to change later: the build matrix, the data directory contract, and the
service lifecycle all get baked into how everything else is written.

## Done when

- All five build targets green in CI with no C compiler present.
- On the chosen host: `bokkie install` → reboot → a second machine **and a
  phone** both load the app shell at `http://bokkie.local:8080`, with nobody
  logged into the host.
- Killing the process mid-request: it restarts and `/healthz` recovers.

---

## Tasks

### 1.1 — Repo scaffold, Go module, Makefile, CI ⭐ do this first

**The cross-compile matrix comes before any other code.** This is the cheapest
possible moment to catch a cgo dependency; once features exist it is far more
expensive to unpick.

Build:

- `cmd/bokkie/main.go` — subcommand dispatch skeleton (`version`, `run`,
  `install`, `uninstall`, `backup`). Only `version` needs to work in this task.
- `internal/buildinfo` — `Version`, `Commit`, `Date` stamped via `-ldflags`, and
  a `String()` that also reports `GOOS/GOARCH`. Worth the ten minutes: the first
  question on any support call is "which build is actually on that machine?"
- `Makefile` — `dev`, `test`, `vet-all`, `build-all`, `clean`, `ci`.
- `.github/workflows/ci.yml` — the same checks the Makefile runs.
- `.gitignore` — `dist/`, `node_modules/`, `*.db`, `*.db-wal`, `*.db-shm`,
  `web/dist/`.

The five targets, all with `CGO_ENABLED=0`:

```bash
GOOS=windows GOARCH=amd64  →  dist/bokkie-win-amd64.exe
GOOS=darwin  GOARCH=arm64  →  dist/bokkie-mac-arm64
GOOS=darwin  GOARCH=amd64  →  dist/bokkie-mac-amd64
GOOS=linux   GOARCH=amd64  →  dist/bokkie-linux-amd64
GOOS=linux   GOARCH=arm64  →  dist/bokkie-linux-arm64      # Raspberry Pi 5
```

Also `go vet` once per `GOOS`, so the build-tagged `printing` and `config` files
are all type-checked rather than only the host's. Without this, a broken
`spooler_windows.go` sits undetected until someone builds for Windows.

> Make `make ci` run the identical checks locally. There is no git remote yet, so
> a workflow file alone would be aspirational; a Make target is useful today.

**Done when:** `make build-all` produces five binaries and `make ci` passes.

---

### 1.2 — `internal/config`

- `config.yaml` loading with sane defaults, so a fresh install runs with no
  config file at all.
- Per-OS data directory in `datadir_windows.go` / `datadir_darwin.go` /
  `datadir_linux.go` (paths in `00-architecture.md` § Operations).
- `--data-dir` override. **Not optional** — the Step 2 restore drill depends on
  pointing a second instance at a scratch directory.
- Settings: port, printer queue name, backup folder, retention count, timezone.

Create the data directory tree (`backups/`, `logs/`, `slips/`) on first run with
restrictive permissions — it will hold the station's entire financial history.

**Done when:** the binary reports its resolved data dir on each OS, and
`--data-dir` overrides it.

---

### 1.3 — Structured logging

- `log/slog` writing to `logs/` with rotation and a retention cap.
- Text handler at a readable level — these logs get read by whoever is standing
  at the machine, not shipped to a log aggregator.
- **No PII in logs.** Customer names and amounts do not belong in a log file
  that gets copied onto a USB stick for debugging. Log IDs, not people.
- Log the resolved config on boot (minus secrets). Half of remote debugging is
  discovering the config isn't what someone assumed.

**Done when:** logs rotate, and a boot line records version + data dir + port.

---

### 1.4 — SQLite bootstrap

Open with the pragmas that make SQLite safe for concurrent use:

```
journal_mode = WAL        -- concurrent readers during a write
foreign_keys = ON         -- off by default in SQLite; a real footgun
busy_timeout = 5000       -- wait rather than instantly failing under contention
synchronous = NORMAL      -- correct and fast with WAL
```

- Embedded `goose` migrations applied on boot from `internal/db/migrations/`.
- **Fail loudly and refuse to start** on a schema newer than the binary
  understands. A downgraded binary silently running against a newer schema is a
  data-corruption path.
- No domain tables yet — only the migration machinery and whatever `goose`
  needs.

**Done when:** a fresh data dir produces a migrated database; a second boot is a
no-op; a deliberately corrupted schema version refuses to start.

---

### 1.5 — HTTP server

- stdlib `ServeMux` with Go 1.22 pattern routing.
- Middleware: panic recovery, request logging, request ID, and a placeholder
  session hook that Step 2 fills in.
- Graceful shutdown on SIGINT/SIGTERM — and the Windows service stop signal,
  which is *not* the same thing and is easy to miss.
- `/healthz` returning version, uptime, and database reachability.
- Bind to `0.0.0.0` so the LAN can reach it; make the port configurable.

**Done when:** the server starts, `/healthz` responds from another machine, and
shutdown drains in-flight requests rather than cutting them off.

---

### 1.6 — Vue SPA scaffold

- Vite + TypeScript + Vue 3 + PrimeVue 4 (Aura) + Pinia + vue-router in `web/`.
- `go:embed` the built `dist/`. **Gotcha:** `go:embed` fails to compile if the
  directory doesn't exist, so commit a placeholder or generate it in the
  Makefile — otherwise a clean checkout won't build.
- Dev mode proxies to the Vite dev server so hot reload works; production serves
  from the embedded FS. One flag switches between them.
- SPA fallback routing: unknown paths serve `index.html`, but `/api/*` must
  404 as JSON rather than returning the HTML shell. Getting this backwards
  produces baffling client errors.
- Just the app shell and a placeholder page. No login form yet — that's 2.1.

**Done when:** `make dev` gives hot reload; the production binary serves the SPA
from memory with no `web/` directory present.

---

### 1.7 — Service install, firewall, mDNS

- `bokkie install` / `uninstall` / `run` via `kardianos/service`, working on
  Windows SCM, launchd, and systemd.
- Run as a dedicated low-privilege account per `00-architecture.md` § Operations.
- Firewall rule added at install time, per OS.
- mDNS advertising `bokkie.local` via `grandcat/zeroconf`, **with the Avahi
  fallback on Linux** — Avahi usually already holds port 5353, so binding a
  second responder fails. Detect and register through Avahi's D-Bus API instead.
- macOS: ad-hoc codesign at build time or strip the quarantine attribute at
  install, or the binary refuses to run with an unhelpful error.

**Done when:** install → reboot → service is up with nobody logged in.
Test this on Windows specifically: **a service that only runs while a user is
logged in is a common and painful mistake**, and it will not be discovered until
the station reboots one morning.

---

## Verification

```bash
make ci          # vet per GOOS + tests + all five builds, CGO_ENABLED=0
make build-all   # five binaries in dist/
```

**All five must succeed with no C compiler installed.** If any target starts
demanding a cross-toolchain, a cgo dependency has crept in — almost certainly the
SQLite driver — and the host flexibility in Decision 5 is quietly gone.

Manual, on the chosen host:

1. `bokkie install`, reboot, confirm the service is running with nobody logged in.
2. Load the app shell from a second PC and from a phone via `bokkie.local`.
3. `kill -9` the process; confirm the service manager restarts it and `/healthz`
   recovers.
4. Delete the data dir, restart, confirm it rebuilds cleanly from nothing.

## Risks

| Risk | Mitigation |
|---|---|
| A dependency pulls in cgo | Task 1.1 CI gate catches it the day it happens |
| Windows service runs only when logged in | Explicit reboot test in verification |
| Avahi port conflict on Linux | Handled in 1.7, not discovered in the field |
| `go:embed` breaks a clean checkout | Placeholder `web/dist` committed |
