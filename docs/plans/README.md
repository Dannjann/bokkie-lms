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
| [02-identity-audit.md](02-identity-audit.md) | Step 2 — auth, roles, audit log, backup | ✅ Built, gate passed |
| [02.5-install-update.md](02.5-install-update.md) | Step 2.5 — install location, signed USB updates, first inch | ⬜ Not started |
| [../runbook.md](../runbook.md) | Operating and restoring, for the owner | ✅ Written |
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

**Step 2 is complete and its gate has passed.** On top of step 1's foundation,
the system now has: argon2id logins with SQLite-backed sessions that survive a
restart, a first-run admin setup that closes for good, `encoder`/`manager` roles
enforced at route registration, an append-only `audit_log` that the schema
itself refuses to edit, a typed `settings` table whose changes take effect
without a restart, and a nightly self-verifying `VACUUM INTO` backup that **has
actually been restored** — service stopped, database deleted, backup swapped in,
everything back including live sessions. See
[02-identity-audit.md](02-identity-audit.md) for the drill results and
[../runbook.md](../runbook.md) for the restore procedure in the owner's language.

**Step 2.5 is planned and not started.** Before Step 3 goes in, the host needs a
fixed install location and a `bokkie update` that backs up, swaps, restarts and
rolls itself back. Updates are manual from a USB stick by decision; the step is
shaped so OTA later adds a download and changes nothing else. See
[02.5-install-update.md](02.5-install-update.md).

**Step 1's manual drill is still outstanding**, and is now the only thing behind.
It needs hardware this project has not had: a second machine and a phone reaching
`bokkie.local` across a LAN, plus a reboot. Windows and Linux hosts remain
unavailable, so SCM, systemd and the Avahi fallback are written but unverified.
That caveat now covers step 2's service-management commands too, and travels to
Step 10.

Everyday commands:

```bash
make setup      # once after cloning: activates the pre-push hook
make web-dev    # Vite with hot reload …
make dev        # … and bokkie proxying to it on its own port
make release    # SPA build + all five binaries
make ci         # the gate

bokkie backup            # a verified backup, safe while the server is running
bokkie backup -list      # what is in the backup folder
bokkie backup -verify F  # check one file, no config or database needed
```
