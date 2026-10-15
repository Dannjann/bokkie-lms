# Bokkie LMS — Planning Docs

Replacement for the Visual FoxPro lending system at **TLS Palay & Corn Buying
Station**, Pintor Gamu, Isabela.

## Reading order

| Doc | What it covers | Status |
|---|---|---|
| [00-architecture.md](00-architecture.md) | Decisions, stack, data model, operations, roadmap | ✅ Approved |
| [adr-001-language-choice.md](adr-001-language-choice.md) | Why Go, and the alternatives weighed against it | ✅ Accepted |
| [domain-interview.md](domain-interview.md) | Questions for the owner — unblocks Steps 7–8 | 🟨 Q1–Q6 answered from the legacy program, to confirm |
| [legacy-findings.md](legacy-findings.md) | What the old FoxPro program and its data say about Q1–Q9 | ✅ Implemented in Step 7.5, to confirm |
| [questions-for-the-owner.md](questions-for-the-owner.md) | The same questions in plain language — the sheet you print and take | ⬜ Not asked |
| [01-foundation.md](01-foundation.md) | Step 1 — build system, runtime, service install | 🟨 Built, gate pending |
| [02-identity-audit.md](02-identity-audit.md) | Step 2 — auth, roles, audit log, backup | ✅ Built, gate passed |
| [02.5-install-update.md](02.5-install-update.md) | Step 2.5 — install location, signed USB updates, first inch | 🟨 Built, gate pending |
| [02.6-github-distribution.md](02.6-github-distribution.md) | Step 2.6 — the Windows setup exe, downloads from GitHub, the download page | 🟨 Built, gate pending |
| [02.6-implementation-plan.md](02.6-implementation-plan.md) | Step 2.6 — the work order it was built from | ✅ Executed |
| [02.7-window-is-the-server.md](02.7-window-is-the-server.md) | Step 2.7 — the window is the server; nothing auto-starts | 🟨 Built, gate pending |
| [02.7-implementation-plan.md](02.7-implementation-plan.md) | Step 2.7 — the work order it was built from | ✅ Executed |
| [../runbook.md](../runbook.md) | Operating and restoring, for the owner | ✅ Written |
| [03-ledger-core.md](03-ledger-core.md) | Step 3 — money type and the ledger spine | ✅ Built, gate passed |
| [04-customers.md](04-customers.md) | Step 4 — customer master, `BA-####` codes, search, CSV import | ✅ Built, gate passed |
| [04-customers-gate.md](04-customers-gate.md) | Step 4 — two questions still owed to the owner | 🟨 Task 1 done |
| [05-advances.md](05-advances.md) | Step 5 — advance vouchers, draft→posted, void via reversal | ✅ Built, gate passed |
| [06-payments.md](06-payments.md) | Step 6 — payments, the principal/interest split, customer statement | ✅ Built, gate passed |
| [07-interest.md](07-interest.md) | Step 7 — the interest run, its preview, and undoing one | ⛔ Superseded by 7.5 |
| [07.5-settlement.md](07.5-settlement.md) | Step 7.5 — one farmer settled to a cut-off; palay payments; plain-English wording | 🟨 Built, screens to walk |
| [08-reports.md](08-reports.md) | Step 8 — the four reports, and the page that leaves the room | ✅ Built, revised by 7.5 |
| [08.5-admin-screens.md](08.5-admin-screens.md) | Step 8.5 — users, audit log, settings, statements index | ✅ Built, gate passed |

Steps 9 and 10 are **features**. They are listed in the roadmap in
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

**Step 4 is complete and its gate has passed.** The system now has
screens, and they have been looked at. The customer master is in: codes assigned by the server inside the
`INSERT` itself, so fifty simultaneous creates get fifty distinct codes;
nothing ever deleted, so no code is handed out twice; server-side search that
treats a typed `%` as a percent sign and finds `0917-123-4567` when somebody
types `0917`; and a CSV import with two front doors over one engine, where the
dry run is not a description of the import but the import minus the commit, and
a bad row at line 300 writes nothing at all. The import cannot post a ledger
entry — it has no way to — so the one bulk write in the system is structurally
unable to put a wrong number into the record. Both mutation checks hold: a
read-then-write allocator and a deactivation that deletes are each caught.

Around those screens there is now a shell: a rail that lists the sections
coming in later steps rather than hiding them, a connection light that
distinguishes "checking" from "down", and one page header every screen shares.
Walking it in a browser at the width the station actually runs — Edge in
`--app` mode at about 1024px — turned up exactly one layout fault, in a 68px
band that is invisible at any convenient width, and it is fixed. PrimeVue is
pinned to 4: version 5 needs a commercial licence and stamps a red "Invalid
PrimeUI License" badge over every page without one. See
[04-customers.md](04-customers.md) for what the gate proved and the six
decisions taken while building, and
[04-customers-gate.md](04-customers-gate.md) for the two questions still owed
to the owner — neither blocks Step 5, and both belong on the same visit as the
domain interview.

**Step 5 is complete and its gate has passed.** The system can lend money. A
voucher is typed as a draft that survives a closed tab, posted once — which is
when it is numbered, from a gap-free sequence continuing the legacy one — and
undone only by reversing every line at once. Posting writes the voucher's
status, its ledger entries and the audit row in a single transaction, which is
what task 5.1 went into the ledger to make possible; and its `UPDATE` runs
before anything is read, because a deferred transaction that upgrades its lock
is what made Step 4's read-then-write mutation fail as `SQLITE_BUSY` on 43 of 50
writers. Both mutations here are caught: a read-then-write allocator seizes the
counter up under twenty posters, and a void that reverses only its first line
leaves two of three farmers holding money they were never lent.

The screens were walked in Chromium against the real binary at the width the
station runs — 1024px — and swept from 700 to 1440 and at 390. They turned up
three faults, all invisible at a convenient width or on a screen that had never
had real money on it: pesos printed ungrouped beside a screen that grouped
them, a reversal printing `₱-500.00`, and a one-pixel hidden label that escaped
a scrolling card and dragged the page 316px wide on a phone. All three are
fixed; see [05-advances.md](05-advances.md) for the seven decisions taken
before building and the seven taken while building.

**Step 6 is complete and its gate has passed.** Money can come back. A payment
is one receipt for one farmer — header only, no grid, because that is what a
counter transaction is — typed as a draft, posted once against its own OR No.
sequence, and undone only by reversing the entry it wrote. Posting is the first
thing in the system that genuinely has to *read*: the split between principal
and interest depends on what the farmer owes at that moment. Step 5's rule
survives intact, because what it requires is that the first statement be a
write — so the order is UPDATE the number and status, read the balance, divide
in Go, post the entry. All three mutations are caught: a read-then-write
allocator seizes up under twenty posters, a split that clears interest without
capping at the amount drives a half negative, and sending an overpayment's
excess to interest fails on the first generated test.

The split itself is stored on the ledger entry and nowhere else, so there is no
second copy to disagree with the record. Which half is cleared first is the
`payment.applies` setting, defaulting to interest-first: Q5 is unanswered and
nothing charges interest until Step 7, so the answer is a settings change rather
than a migration, and Q5 no longer blocks anything. Overpayment is allowed and
warned about before posting rather than after, and a deactivated farmer can
still pay — which inverts Step 5's check on purpose, because deactivation says
who the station lends to today and not who owes money from last season.

And a farmer can now be read their own history: a statement with an opening
balance, every entry, a running balance computed in Go on each row, and a
closing balance that must equal `BalanceAt` — an invariant the property tests
check at a cut date as well as at the end. The ledger still knows nothing about
vouchers or receipts; turning an entry's provenance into "Ref. No. 1425" happens
above it, one query per source table. The screens turned up three faults, all
invisible without real money on them — including a handler that reported genuine
faults as bad date ranges. See [06-payments.md](06-payments.md).

**Step 7 is complete and its gate has passed.** The system charges interest. A
run is a document — a header with a line per borrower, drafted, posted once,
undone in one act — because it is computed from the record rather than typed off
a slip, against every farmer at once, and somebody has to be accountable for it.
Q1 is still unanswered and the step was built anyway: almost nothing that
question decides is structure, so the rule is one pure function chosen by a
setting whose default charges nothing, and a fresh install computes no interest
until a manager sets a rate. Two answers would still cost code — interest
pro-rated on early repayment, and a rate negotiated per customer — and both are
written down where the next person will find them.

The draft *is* the preview: the run minus the commit, which is Step 4's CSV rule
restated, because a preview computed by different code than the thing it
previews will disagree in the direction nobody checks. What a draft cannot
promise is that the world stood still, so posting recomputes every line inside
its own transaction and refuses on any difference, naming the farmer and both
figures. Charging a period twice is guarded twice — a unique index for the
identical case, and a condition inside the posting `UPDATE` for the overlapping
one — and building it proved both halves are load-bearing: with the guard
removed, an overlapping period charges silently and an identical one fails with
a constraint error nobody could read.

All four mutations are caught: a read-then-write allocator seizes up under
twenty posters, rounding down instead of half-up fails three rows of the table
test, a void that reverses only its first line leaves the second farmer charged,
and removing the period guard charges September twice. The screens found three
faults, one of them older than this step — every `label for` on the PrimeVue
date pickers and selects, here and in Step 6, addressed a wrapper rather than
the input inside it. And `payment.applies`, shipped in Step 6 with nothing to
exercise it, finally divides a real payment against real interest. See
[07-interest.md](07-interest.md).

**Step 7.5 replaced the interest run with the settlement**, because the legacy
program answered Q1–Q6 once somebody read it. The station never charged
interest across every farmer at once. It settled one farmer at a time when they
delivered palay: interest on each open advance from its own date to a cut-off,
at a monthly rate typed on the day (5% almost always), simple not compound, and
the delivery applied interest first. The settlement reproduces the legacy
formula to the centavo on the legacy program's own rows. A payment is now
*recorded* at the counter, palay lot by lot, and applied to the balance only
when the farmer is settled. ROI's table is the legacy one, and "Liquidated" is
now **Settled**, a list of settlements that open and reprint. The screens say
*advance slip*, *settlement* and *payment*. `refno.start` defaults to 1426,
continuing the legacy sequence. See [07.5-settlement.md](07.5-settlement.md)
and [legacy-findings.md](legacy-findings.md). The screens still have to be
walked at 1024px, as every step since 5 has needed.

**Step 8 is complete and its gate has passed.** The system produces paper. Four
reports — who owes money, whether the lending is working, how old the debt is,
and who has finished paying — each a date question, all manager-only, all
printed through the browser. Q3 and Q4 were unanswered and were answered by
assumption in the open: ROI is a portfolio block with a per-farmer table under
it, and Liquidated is a view built so it structurally cannot be anything else —
the report package has no write path at all.

Building it corrected the plan on the point that matters. Netting a voided
document by *what* it undid rather than by *when* — which is right for an
interest charge, and what Step 7 does — is wrong for a report: the ledger dates
a reversal the day of the void precisely so an old statement stays reproducible,
and netting a March void out of February's figures breaks that from the other
side. The report reads bound the reversal to their own cut-off, so a report over
a period reads as the books read at the end of it, and Deployed and Outstanding
on the same page tell the same story.

Aging is derived at read time and stored nowhere, taking every positive
principal movement as a lot and applying every negative one FIFO — so an opening
balance at cutover ages from its own date and no entry type can fall silently
outside it. The property test earned its keep immediately, finding that an
overpayment's credit was discarded rather than carried, at a counterexample of
one centavo owed and two aged.

Both mutations are caught: counting a reversal twice turns the amount advanced
negative, and a bucket boundary moved by a day fails the thirty-day case alone.
The screens reconcile three ways on a seeded book, and printing them found five
faults that do not exist on a screen — the topbar, a title printed twice, blue
links on paper, columns aligning one way in print and another on screen, and
grey ink that barely prints. See [08-reports.md](08-reports.md).

**The station can shut the door, and that is not a step.** Between Step 8 and
Step 9 the app grew one control the roadmap never asked for, because the need
turned up before the roadmap did: a manager can switch network access off and
have this machine be the only one the app answers. It is a setting, so it moves
without a restart; switching it off withdraws `bokkie.local` as well, because a
phone that resolves the name and is then refused looks like a fault while a
phone that cannot find the name looks like what it is. The switch itself is
host-only — pressing it from a counter PC would take away the app and the switch
in one click — and the copy says plainly that it decides who is served rather
than who can reach the machine. The port stays open either way; that door is the
WiFi password's job. See the runbook for the owner's version, and
[00-architecture.md](00-architecture.md) under Operations for what it is and is
not.

The customer record gained its history in the same pass: the last ten movements
with their running balance, read from the same statement the statement screen
reads, because a shorter list computed a different way is a list that can
disagree with the paper a farmer is holding.

**Step 8.5 is complete and its gate has passed.** The system can be
administered from inside itself. The four rail entries that had been greyed out
since Step 4 are live: a roster where an account can be created, turned off,
promoted, demoted and given a new password; the audit log — the record the whole
of Step 2 was built to produce — read through a browser at last, narrowed by
person, action, record or week, each row opening to show what changed; the
eleven settings with the backup folder under them; and an index over the
per-customer statements.

Two findings shrank the work before it started. `withSession` already re-reads
the account on every request and ends the session when it finds it inactive, so
deactivation and role changes need no session sweeping — only a password change
does, because a changed hash leaves session rows untouched. And
`routes_test.go` already refuses a mutating route that has not chosen an audit
style, so every new endpoint is audited by construction.

**The plan predicted the wrong mutation and the build corrected it.** Counting
the managers before the `UPDATE` rather than after does not lose the last one:
inside a transaction that is a read-then-write, and WAL hands the second writer
a snapshot conflict it cannot recover from. It is safe but unusable — nineteen
of twenty writers die on the conflict, which the API could only report as a 500.
What actually loses the station its manager is taking the count *outside* the
transaction, and held open at a barrier that loses both managers on every run.
The write-first ordering stays for the second reason as well as the first: it is
the only shape that turns the losers into a sentence somebody can read.

The audit filters turned up a subtler one. `at` is written with
`time.RFC3339Nano`, which drops trailing zeros from the fraction, so comparing a
filtered day against a full RFC3339 midnight is wrong in the direction nobody
checks — `.` sorts before `Z`, and two of the first three entries of a day
silently vanish. And the `LIKE` mutation is not the one it looks like: a dot is
literal in SQL `LIKE`, so the first version of that test passed against the bug.
What `LIKE` leaks is `_` and ASCII case.

**Seven faults were found by walking the screens and none by a test**, which is
the pattern every step since 5 has repeated. Three text buttons in a row clipped
"Deactivate" off the card at 1024px — the width the station actually runs — and
a page-overflow check sees nothing, because the table clips inside the card
rather than making the document wider. The same CSS mistake was made twice:
`flex: 1 1 18rem` written for a row is a width, and the moment a narrow-width
rule turns that row into a column the identical declaration is a *height*. And a
backup's file name is UTC by decision so that it sorts in any file manager,
while the panel printed its modification time beside it — eight hours apart, on
the one screen whose job is to say whether last night's backup ran. Modification
time was the wrong source anyway, as `backup.List` already said in a comment: a
file copied off a USB stick and back records the copy, not the backup.

Two of those are now tests rather than promises. The audit and settings
vocabularies are read out of the TypeScript and checked against the route
registry and `settings.All` in both directions — a Go test parsing a `.ts` file,
in the spirit of the ledger's no-floats test. The first caught a real gap
immediately: the `Change.Action` override meant `user.deactivated` was written
by a handler and appeared nowhere in the registry, so `Route.AlsoWrites` now
declares it.

One decision is worth knowing about. **The statements index is registered
`Authenticated`, not `ManagerOnly`, which departs from the four reports.** They
are gated because they are portfolio views and an index across every farmer's
balance is arguably one too; it is open because the person who has to tell a
farmer what they owe is the encoder at the counter, and any single farmer's
balance is already one click away on the customer screen. The reasoning is on
the handler and the decision is pinned by a test that also checks the aging
report is still shut, so it cannot pass because the gate came off entirely. See
[08.5-admin-screens.md](08.5-admin-screens.md).

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
make install-local   # put this tree on this machine's service (ARGS='--dry-run')
make release    # SPA build + all five binaries — refuses on an untagged commit
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

```bash
go test ./internal/domain/payment/... -race    # the receipt, the split, twenty posters
go test ./internal/domain/settlement/... -race # the legacy formula, to the centavo
go test ./internal/domain/report/... -race     # the four reports, and the FIFO walk
```
