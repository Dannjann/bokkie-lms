# Bokkie LMS — Planning Docs

Replacement for the Visual FoxPro lending system at **TLS Palay & Corn Buying
Station**, Pintor Gamu, Isabela.

## Reading order

| Doc | What it covers | Status |
|---|---|---|
| [00-architecture.md](00-architecture.md) | Decisions, stack, data model, operations, roadmap | ✅ Approved |
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

## Current state of the repo

Scaffolding only: `go.mod` (module `bokkie-lms`, Go 1.24) and an initialised git
repo with no commits. Go 1.27.1 is installed on the dev machine. **No
application code has been written yet** — that begins at task 1.1.
