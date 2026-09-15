# Step 3 — Money and the Ledger Core

**Status:** not started · **Depends on:** Step 2 · **Blocks:** every feature step

---

## Goal

**The architectural spine.** Every feature from Step 4 onward is a read or a
write against this. It ships with **no screens** — it is proven by tests, not by
clicking.

This is the part of the system that must still be right in ten years, when a
farmer disputes a balance from three seasons ago and the answer has to be
reconstructible from the record rather than remembered.

## Done when

Arbitrary sequences of posts and reversals produce provably correct, rebuildable
balances under property testing. Nothing is visible in the UI — that is expected
and correct.

---

## Ledger semantics

Get these right on paper before writing code; they are the contract every later
feature codes against.

### Sign convention

Deltas are **from the lender's perspective**: positive increases what the
customer owes.

| Entry type | `principal_delta` | `interest_delta` | Written by |
|---|---|---|---|
| `opening` | + | + | Cutover (Step 10) |
| `advance` | + | 0 | Posting a voucher (Step 5) |
| `interest` | 0 | + | Interest run (Step 7) |
| `payment` | − | − | Payment allocation (Step 6) |
| `adjustment` | ± | ± | Manager only, reason required |
| `reversal` | negation of target | negation of target | Voiding anything |

Principal and interest are tracked **separately, never netted**. The legacy
customer record kept them as separate columns, which tells us the business
thinks of them separately — and Q5 of the domain interview (which one a payment
clears first) only has meaning if they stay distinct.

### Immutability

Nothing is ever updated or deleted after posting. A void writes a *reversing
entry*.

Enforce this **in the database, not only in Go** — SQLite triggers that `RAISE`
on `UPDATE` or `DELETE` against `ledger_entries`. Application-layer discipline
is not enough for the one table where a mistake is unrecoverable, and a trigger
also protects against someone poking at the file with a SQLite browser.

### Reversal rules

- A reversal's deltas are the exact negation of its target.
- The target's `reversed_by_id` is set to the reversal's id, in the same
  transaction.
- **An entry can be reversed at most once** — enforce with a unique index on
  `reversed_by_id`, not just a Go check.
- **A reversal cannot itself be reversed.** Correcting a mistaken void means
  posting a fresh entry, which leaves a legible trail of what actually happened.
- A reversal carries its own `created_by` and a reason.

### Balance derivation

```sql
SELECT SUM(principal_delta_cents), SUM(interest_delta_cents)
FROM ledger_entries WHERE customer_id = ?
```

That is the truth. `customer_balances` is a cache and nothing else.

---

## Tasks

### 3.1 — The `Money` type

- `int64` centavos. Never `float64`, anywhere, for any reason.
- JSON marshals as a decimal **string** (`"1234.50"`), so JavaScript's float
  arithmetic can never touch a peso amount in transit. The frontend formats and
  displays it; it does not do arithmetic on it.
- Arithmetic with explicit overflow checks. `int64` centavos overflows at ~92
  quadrillion pesos so it will not happen in practice, but a silent wrap is the
  kind of bug that is impossible to diagnose after the fact.
- **No division without an explicit rounding mode.** Interest calculation will
  need this in Step 7, and "how did we round?" must be a decision recorded in
  code, not an accident of the language.
- Parse from user input tolerantly (commas, spaces, `₱`) and reject ambiguity
  loudly.

**The float ban needs a test, not a convention.** Write a test that parses the
AST of the money-handling packages and fails if any `float32`/`float64`
declaration, conversion, or literal appears in a money path. A code review rule
will eventually be forgotten; a failing build will not.

**Done when:** `Money` round-trips through JSON unchanged, and the AST test fails
when a float is deliberately introduced.

---

### 3.2 — Ledger schema

Migration only. No handlers, no API.

- `ledger_entries` and `customer_balances` per `00-architecture.md` § Data model.
- Indexes on `(customer_id, entry_date)` for statements and
  `(source_table, source_id)` for provenance lookups.
- `CHECK` constraints on `entry_type` and on reversal consistency.
- The immutability triggers described above.

**Done when:** migrations apply cleanly; an attempted `UPDATE` or `DELETE` on
`ledger_entries` fails at the database level.

---

### 3.3 — Ledger service

The complete API every later feature will use. Keep it small:

```go
Post(ctx, entry) (ID, error)          // append one entry
PostBatch(ctx, entries) error         // several entries, one transaction
Reverse(ctx, entryID, reason) error   // write the negation, link the target
BalanceFor(ctx, customerID) (Balance, error)
BalanceAt(ctx, customerID, date) (Balance, error)   // reports need as-of
StatementFor(ctx, customerID, from, to) ([]Entry, error)
RebuildCache(ctx) error               // recompute customer_balances from scratch
```

- `PostBatch` matters: posting a voucher spanning several customers (Q6) must be
  atomic. Half a posted voucher is a corruption, not an error.
- `BalanceAt` exists because reports are historical. Retrofitting as-of queries
  onto a service that only knows "now" is painful; the ledger makes it free.
- Cache updates happen in the same transaction as the entry, never in a
  background job that can drift.

**Done when:** the service is exercised end-to-end by tests with no HTTP layer
involved.

---

### 3.4 — Property tests

The most valuable tests in the codebase. Generate random sequences of posts and
reversals across several customers, then assert invariants:

1. **Derived == rebuilt.** The cached balance always equals a from-scratch
   `RebuildCache`. This is the invariant that catches nearly everything.
2. **Reversal restores exactly.** Posting entry E then reversing it returns the
   balance to precisely its prior value — not within a centavo, exactly.
3. **Order independence.** The same set of entries in any posting order yields
   the same final balance.
4. **Conservation.** The sum of all deltas across all customers equals the sum
   of everything ever posted, minus everything reversed.
5. **Double-reversal is refused.** Reversing an already-reversed entry errors and
   changes nothing.
6. **Atomicity.** A `PostBatch` that fails partway leaves no entries at all.

Use `pgregory.net/rapid` or stdlib `testing/quick`. Rapid's shrinking gives much
smaller failing cases, which matters when a counterexample is a 200-entry
sequence.

**Done when:** all six invariants hold over thousands of generated sequences.

---

### 3.5 — Startup self-check

- On boot, compare `customer_balances` against a fresh aggregation of
  `ledger_entries`.
- On mismatch: log loudly with the affected customer IDs, rebuild automatically,
  and record the event in `audit_log`.
- Skip the check above a row threshold and run it on a schedule instead, so
  startup stays fast once the ledger has years of history in it.

A mismatch should be impossible given 3.3's transactional cache updates. Check
anyway — the cost is negligible and the failure mode it catches is "the numbers
have been quietly wrong for a while", which is exactly what the legacy
carry-forward balance fields did.

**Done when:** deliberately corrupting `customer_balances` is detected on boot,
logged, rebuilt, and audited.

---

## Verification

```bash
go test ./internal/domain/ledger/... -race
go test ./... -run Property -count=10
```

- All six property invariants hold.
- Money round-trips through JSON unchanged; the AST float test fails when a
  float is deliberately introduced.
- `UPDATE`/`DELETE` on `ledger_entries` is rejected by the database.
- Corrupted cache is detected at startup, rebuilt, and audited.
- Concurrent posts under `-race` produce correct balances.

## Risks

| Risk | Mitigation |
|---|---|
| A float creeps into a money path | AST test in CI, not a review convention |
| Cache drifts from the ledger | Same-transaction updates + startup self-check |
| Partial voucher posting | `PostBatch` atomicity, covered by property test 6 |
| Someone edits the DB with a SQLite browser | Immutability triggers in the schema |
| Reports later need as-of balances | `BalanceAt` built now, while it is free |

---

## What this step deliberately does not do

No customer records, no advances, no payments, no UI. Those are Steps 4–6, each
with its own plan. If a lending concept starts appearing in `internal/domain/ledger`,
the boundary has been crossed — the ledger knows about entries and balances, and
nothing about palay.
