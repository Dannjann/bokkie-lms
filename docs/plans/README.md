# Bokkie LMS — Planning Docs

Replacement for the Visual FoxPro lending system at **TLS Palay & Corn Buying
Station**, Pintor Gamu, Isabela.

## Reading order

| Doc | What it covers | Status |
|---|---|---|
| [00-architecture.md](00-architecture.md) | Decisions, stack, data model, operations, roadmap | ✅ Approved |
| [adr-001-language-choice.md](adr-001-language-choice.md) | Why Go, and the alternatives weighed against it | ✅ Accepted |
| [domain-interview.md](domain-interview.md) | Questions for the owner — unblocks Steps 7–8 | ⬜ Not started |
| [01-foundation.md](01-foundation.md) | Step 1 — build system, runtime, service install | 🟨 Built, gate pending |
| [02-identity-audit.md](02-identity-audit.md) | Step 2 — auth, roles, audit log, backup | ⬜ Not started |
| [03-ledger-core.md](03-ledger-core.md) | Step 3 — money type and the ledger spine | ⬜ Not started |

Steps 4–10 are **features**. They are listed in the roadmap in
`00-architecture.md` for shape only — each gets its own plan doc, written when
we reach it, not now.

## How these docs are meant to be used

- `00-architecture.md` is the reference. It should change rarely, and only on a
  deliberate decision.
- Each step doc is a work order: task table, gotchas, and a **done-when** gate.
  A step is not finished until its gate passes.
- `domain-interview.md` is a physical artifact — print it, take it to the owner,
  fill it in, commit the answers.

## Decision records

Architecture decisions that were genuinely contested get an ADR, so the
reasoning survives the conversation that produced it. An ADR records the
alternatives and the conditions under which the decision should be reopened —
it is not a defence of the choice.

## Current state of the repo

**Step 1 is built end to end; its gate has not been run.** All seven tasks are
in: the five-target build matrix, `internal/config` (per-OS data dir,
`config.yaml`, `--data-dir`), daily rotating `slog` files, SQLite with WAL and
embedded `goose` migrations, the HTTP server with `/healthz` and graceful
shutdown, the Vue SPA embedded via `go:embed`, and `bokkie install` registering
a service with a dedicated account, a firewall rule and mDNS.

`make ci` passes — five binaries, `CGO_ENABLED=0`, `go vet` per GOOS. What is
left is the manual drill in [01-foundation.md](01-foundation.md): install on a
host, reboot, and load the app from a second machine and a phone over
`bokkie.local`. Windows and Linux hosts remain unavailable, so SCM, systemd and
the Avahi fallback are written but unverified — that caveat travels to Step 10.

Everyday commands:

```bash
make setup      # once after cloning: activates the pre-push hook
make web-dev    # Vite with hot reload …
make dev        # … and bokkie proxying to it on its own port
make release    # SPA build + all five binaries
make ci         # the gate
```
