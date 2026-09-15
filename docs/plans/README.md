# Bokkie LMS — Planning Docs

Replacement for the Visual FoxPro lending system at **TLS Palay & Corn Buying
Station**, Pintor Gamu, Isabela.

## Reading order

| Doc | What it covers | Status |
|---|---|---|
| [00-architecture.md](00-architecture.md) | Decisions, stack, data model, operations, roadmap | ✅ Approved |
| [adr-001-language-choice.md](adr-001-language-choice.md) | Why Go, and the alternatives weighed against it | ✅ Accepted |
| [domain-interview.md](domain-interview.md) | Questions for the owner — unblocks Steps 7–8 | ⬜ Not started |
| [01-foundation.md](01-foundation.md) | Step 1 — build system, runtime, service install | ⬜ Not started |
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

**Step 1 in progress.** Task 1.1 is done: module
`github.com/Dannjann/bokkie-lms` (Go 1.27), the `bokkie` subcommand skeleton
(`version` works, the rest name their task), `internal/buildinfo` stamped via
`-ldflags`, and a Makefile producing all five cross-compiled binaries with
`CGO_ENABLED=0`. `make ci` is the gate and `.githooks/pre-push` runs it — there
is deliberately no hosted CI. Next: task 1.2, `internal/config`.

Run `make setup` once after cloning to activate the hook.
