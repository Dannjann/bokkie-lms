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
| [02.5-install-update.md](02.5-install-update.md) | Step 2.5 — install location, signed USB updates, first inch | 🟨 Built, gate pending |
| [../runbook.md](../runbook.md) | Operating and restoring, for the owner | ✅ Written |
| [03-ledger-core.md](03-ledger-core.md) | Step 3 — money type and the ledger spine | ✅ Built, gate passed |
| [04-customers.md](04-customers.md) | Step 4 — customer master, `BA-####` codes, search, CSV import | 🟨 Built, gate pending |
| [04-customers-gate.md](04-customers-gate.md) | Step 4 — the three things left to close its gate | ⬜ Not started |

Steps 5–10 are **features**. They are listed in the roadmap in
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

**Step 3 is complete and its gate has passed.** The spine is in: a `Money` type
that is int64 centavos and marshals as a string, with a test that parses the
syntax tree of the money packages and fails on any float; an append-only
`ledger_entries` the database itself refuses to update or delete; reversals that
are the exact negation of their target, at most once, never of another reversal,
and dated the day of the void so an old statement stays reproducible; a small
service every later step will code against; six property invariants holding over
thousands of generated sequences; and a boot-time self-check that rebuilds and
audits a cache found disagreeing with the entries behind it. Nothing is visible
in the UI, which is what this step was for. See
[03-ledger-core.md](03-ledger-core.md) for the decisions taken while building —
two of them amend what the plan originally said.

**Step 4 is built and one line of its gate is outstanding.** The system now has
screens. The customer master is in: codes assigned by the server inside the
`INSERT` itself, so fifty simultaneous creates get fifty distinct codes;
nothing ever deleted, so no code is handed out twice; server-side search that
treats a typed `%` as a percent sign and finds `0917-123-4567` when somebody
types `0917`; and a CSV import with two front doors over one engine, where the
dry run is not a description of the import but the import minus the commit, and
a bad row at line 300 writes nothing at all. The import cannot post a ledger
entry — it has no way to — so the one bulk write in the system is structurally
unable to put a wrong number into the record. Both mutation checks hold: a
read-then-write allocator and a deactivation that deletes are each caught. What
is left is looking at the screens in a browser at counter-PC resolution. See
[04-customers.md](04-customers.md) for what the gate proved and the four
decisions taken while building, and
[04-customers-gate.md](04-customers-gate.md) for the three things left —
looking at the screens, and two questions for the owner.

**Step 2.5 is built and its gate is pending.** The host now installs to one
fixed path, and `bokkie update` takes a signed release folder, verifies it,
backs the database up, swaps the binary, restarts, confirms `/healthz` answers
with the new version, and puts the old binary back if it does not. Install and
update are each a double-click. Updates are manual from a USB stick by
decision; the step is shaped so OTA later adds a download and changes nothing
else.

**Two things must happen before the first release:** `make keygen`, with the
public half pasted into `internal/release/verify.go` and the private half
backed up off the machine — until then `PublicKey` is empty and every update is
refused by design — and a first version tag, without which `make release`
refuses to run. See [02.5-install-update.md](02.5-install-update.md).

**Step 1's manual drill is still outstanding**, and remains the only thing behind.
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

go test ./internal/domain/ledger/... -race     # the ledger, including concurrent posts
go test ./internal/domain/customer/... -race  # codes, under fifty writers at once
go test ./... -run Property -count=10         # every invariant, harder

bokkie backup            # a verified backup, safe while the server is running
bokkie backup -list      # what is in the backup folder
bokkie backup -verify F  # check one file, no config or database needed
bokkie backup -set-folder F   # point backups at a USB stick, checked before saving

bokkie update -check DIR # is that folder a genuine release, and what is in it
bokkie update DIR        # back up, swap, restart, verify — or roll back

bokkie import-customers -check F.csv  # what the file would do, writing nothing
bokkie import-customers F.csv         # load the customer master, all or nothing
```
