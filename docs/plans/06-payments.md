# Step 6 — Payments

**Status:** ✅ built, gate passed · revised by [07.5-settlement.md](07.5-settlement.md) · **Depends on:** Step 5 · **Blocks:** Steps 8, 10

> **Revised, 2026-10-15.** Posting a payment now *records* it: it takes its OR
> No. but does not move the balance. A settlement applies it, interest first,
> after charging interest to the cut-off — the legacy rule. A palay payment
> carries lines of class, sacks, kilos and price per kilo, and its amount is
> their sum. The split described below still exists (`payment.Split`); the
> settlement is what calls it.

---

## Goal

**The first thing in the system that moves money back.** After Step 5 a balance
only ever goes up: the station can lend, and a farmer who repays has nowhere to
be recorded. This step builds the document that writes the entries going the
other way — the legacy system's "Payments From Customer" — and the screen that
finally shows a farmer their own history: the customer statement.

It is also the step that decides, once, how a peso coming in is divided between
principal and interest. Nothing in the system charges interest yet, so every
payment posted before Step 7 is entirely principal whatever the rule says. That
is precisely why the rule is cheap to settle now and expensive to settle later.

## Done when

An encoder can take a payment at the counter, save it half-finished and come
back to it, and post it — after which the farmer owes exactly what they owed
minus what they handed over, to the centavo, the receipt carries a number from
the station's own OR sequence, and nothing about it can be edited again. A
manager can void it, after which the balance is exactly back, with both the
payment and its reversal still on the record. And anyone can open a customer and
read a statement whose closing balance is the balance the rest of the system
shows.

---

## Where this stands

All nine tasks are built and the gate has been run, screens included.

| Checked | Result |
|---|---|
| Six property invariants over generated sequences, both rules | ✅ hold |
| 20 concurrent posts under `-race` | ✅ 20 distinct consecutive OR Nos. |
| Two simultaneous posts of one draft | ✅ posted once |
| A voided payment | ✅ both halves of the balance exactly back |
| A second void, and voiding a draft | ✅ both refused |
| Editing or deleting a posted payment | ✅ refused by the package, the API and the database |
| `UPDATE`/`DELETE` on a posted payment's row, by hand in SQL | ✅ refused |
| An edit smuggled in alongside a status change | ✅ refused |
| A `voided → draft` transition, by hand in SQL | ✅ refused |
| A duplicate OR No., by hand in SQL | ✅ refused |
| A rolled-back post | ✅ no entry, no cache movement, still a draft |
| `orno.start` moved to 5000 after the sequence began | ✅ ignored, as designed |
| A payment from a deactivated customer | ✅ accepted, and clears the debt |
| A payment naming somebody not on the books | ✅ refused |
| `payment.applies` set to a misspelling | ✅ refused, naming both legal values |
| An encoder voiding | ✅ refused, refusal audited |
| A caller supplying `or_no` or `status` | ✅ refused at the edge |
| Overpayment | ✅ allowed, warned about before posting, credit on principal |
| **The screens, in a browser, at counter-PC resolution** | ✅ **see below** |

**Both mutations hold**, and each was checked against a deliberate break:

- A read-then-write OR No. allocator fails the concurrency test, and fails
  exactly as Step 4 predicted — `database is locked (SQLITE_BUSY)` on the
  second of twenty posters, not two receipts sharing a number.
- A split that clears interest without capping at the payment amount fails
  three rows of the table test. Worth recording that the plan guessed wrong
  about *which* invariant catches it: the halves still add up to the amount,
  because the second half is computed as the remainder. What breaks is that one
  of them goes negative.
- A third, added while writing the property test: sending an overpayment's
  excess to interest rather than to principal fails on the first generated
  test — *"cleared 1 of interest, more than the 0 owed"*.

**The screens were walked** in Chromium against the real binary with the real
embedded SPA, signed in as a manager and again as an encoder, at 1024px and
then swept from 700 to 1440px and at 390px. The everyday path holds: a farmer
owing ₱5,000.00, a ₱2,000.00 payment typed, saved as a draft, posted as OR No.
1, the balance down to ₱3,000.00, a statement showing both documents by their
numbers and closing where the customer screen says it does, then voided with a
reason and the balance exactly back. Nothing scrolls sideways at any width and
no console errors appear anywhere. Three faults it found are in *Decisions and
faults from building* below.

**The interest half of the split was exercised** before Step 7 exists, by
writing an interest entry into the ledger by hand, letting the boot-time
self-check rebuild the cache it disagreed with — which it did, and audited — and
posting a ₱1,000.00 payment against it. It divided on screen into −₱600.00 of
interest and −₱400.00 of principal.

---

## Decisions and faults from building

**1. The statement handler reported faults as bad requests.** Every failure
from `Statement` came back as *"that date range does not make a statement"*,
including the ones that are not the caller's fault at all. A ledger row with an
unreadable timestamp — somebody repairing the database in a SQLite browser,
which is the realistic case — produced that sentence and no log line, which
would send the first person who hit it to look at the date pickers. Only
`ErrInvalidEntry` is a 400 now; everything else is a 500 and a logged error,
and there is a test for each answer.

**2. The amount field was left-aligned beside a column of right-aligned
pesos.** PrimeVue's `InputText` *is* the input element, so the `:deep(input)`
selector matched nothing. Invisible until there was a balance panel next to it
to be out of line with.

**3. Every receipt said "Cleared ₱0.00 of interest".** True, and useless:
nothing charges interest until Step 7, so that zero was on every receipt in the
system. Only the halves that actually moved are named now.

**4. `BalanceForTx` had to be added to the ledger.** Not in the plan, and
unavoidable: the split must be made from the balance that is true inside the
posting transaction, not one read on another connection before a second
poster's entry landed.

**5. The property test's model tracks only the combined debt.** A model that
computed the split too would be a second copy of `Split`, and a second copy
agrees with a wrong original. Writing the invariants as properties instead
caught a mistake immediately — and it was in the *test*, not the code: under
principal-first with no principal owed, a payment correctly falls through to
interest, and the assertion said it should not.

---

## Decisions settled before building

**1. One receipt, one customer.** No line grid. A payment is a farmer at the
counter handing money over and being given a receipt; a receipt covering three
payers is not a thing that happens. This is the one place Step 6 deliberately
does *not* mirror Step 5 — the advance voucher has a grid because the legacy
screen has a grid, and the legacy payment screen does not.

**2. Allocation means against the balance, not against particular vouchers.**
A payment reduces what the customer owes. It does not settle Ref. No. 1421 and
then Ref. No. 1423. The ledger was built on the sentence "a balance is the sum
of the entries", and a `payment_allocations` table would be a second source of
truth beside it — one that a void then has to unwind, and one that can disagree
with the ledger while looking authoritative. Step 8's aging of outstanding
advances can be derived FIFO from the entry stream when it is known what the
report actually needs (Q3), and deriving it stores nothing that can drift.

**3. The split rule is a setting, and the default is interest-first.** Q5 of the
domain interview — which is cleared first when a farmer owes both — is
unanswered and is not worth stalling the step for, because no payment posted
before Step 7 can be affected by the answer. `payment.applies` holds
`interest-first` or `principal-first`; the owner's answer becomes a settings
change rather than a migration.

The setting needs a kind the store does not have yet: a choice among a fixed
list. `KindChoice` is added with the setting, so a typo in the settings screen
is refused at the edge rather than quietly meaning "interest-first" — this is a
setting where a typo changes what money does.

**4. The split is stored in the ledger entry, and nowhere else.** A payment
entry carries a negative principal delta and a negative interest delta in one
row; `ledger.Entry.Validate` already accepts exactly that shape and refuses
anything positive. So the `payments` row holds the amount and not the division
of it, and the receipt screen reads the division back through
`EntriesBySource`, the way the voucher screen already reads what posting wrote.
Columns on `payments` would be a copy, and a copy of a number is a number that
can disagree with the original.

**5. A payment keeps the draft state.** Four fields is not much to type and the
draft looks like ceremony — but the draft is what makes `UPDATE … WHERE id = ?
AND status = 'draft'` possible, and that `WHERE` clause is what makes a
double-clicked Post button post once. A create-and-post endpoint has no such
guard, and a payment credited twice is worse than an advance posted twice: the
farmer's balance is wrong in the direction nobody complains about.

**6. Overpayment is allowed, and warned about.** A payment larger than the
balance puts the customer in credit, and the remainder lands on principal.
Refusing it would block a payment typed in before the advance it belongs to,
which is an ordinary Tuesday at a counter with a stack of slips. The screen says
so before posting — *"₱2,000.00 more than BA-0042 owes — they will be in
credit"* — because a warning that arrives before the irreversible act is worth
more than a rule that prevents a legitimate one.

**7. A method, in pesos only.** `cash`, `palay` or `other`, plus a free-text
particular. No kilos and no price per kilo. Q2 — whether repayment is cash or
palay netted against the advance, and who computes the delivery value — is
unanswered, and if the answer is "we compute it here" then what is needed is a
delivery document that produces a peso figure and feeds a payment, not two more
columns on this table. Recording the method costs one column and makes the
question answerable from real data when someone finally asks it.

**8. Its own number sequence.** `or_no` mirrors `ref_no` exactly: NULL until
posted, allocated inside the posting `UPDATE`, a partial unique index behind it,
and an `orno.start` setting so the sequence continues the legacy one at cutover.
Separate from the Ref. No. sequence, because they are separate pads of paper.

**9. A deactivated customer can still pay.** This is where Step 5's rule
inverts, and the inversion is the point. `advance.checkCustomers` refuses to put
a deactivated farmer on a draft voucher, because deactivation says who the
station lends to today. It says nothing about who owes money from last season —
and a farmer who is deactivated *because* the relationship ended is exactly the
farmer most likely to be settling up. Refusing their payment would leave a debt
on the books with no way to clear it except an adjustment, which is a correction
entry standing in for an ordinary event. A customer who is not on the books at
all is still refused.

**10. The statement is per-customer and computed on the server.** Opening
balance, the entries in print order, a running balance on every row, closing
balance. No peso figure in this system is added up in JavaScript, and a running
balance is the most tempting place in the whole application to break that rule.

---

## Payment semantics

### Lifecycle

```
draft ──post──> posted ──void──> voided
```

The same three states as a voucher, the same two transitions, the same absence
of an un-post and a re-open. Correcting a posted payment means voiding it and
entering a new one.

A draft has no OR No., has moved no money, and can be edited or deleted
outright. A posted payment is as fixed as the ledger entry behind it. A voided
payment and its reversal both stay on the record: what happened is that money
came in and then went back out, not that it never came in.

### OR No. allocation

Allocated inside the posting `UPDATE`, never as a read followed by a write:

```sql
UPDATE payments
   SET or_no = (SELECT COALESCE(MAX(or_no), ? - 1) + 1 FROM payments),
       status = 'posted', posted_at = ?, posted_by = ?, posted_by_username = ?
 WHERE id = ? AND status = 'draft'
```

`MAX` ignores the NULLs the drafts carry, so the sequence counts posted receipts
only and has no gaps in it. `orno.start` therefore has an effect only while
nothing has been posted — after that `MAX` wins — which is the same deliberate
behaviour as `refno.start` and for the same reason: a setting that can renumber
a live sequence is a footgun.

### Posting writes, then reads, then writes

Step 5's rule was "the `UPDATE` must be the transaction's first statement",
because `internal/db` opens transactions deferred and a transaction that reads
before it writes has to upgrade its lock — which SQLite refuses immediately,
without honouring `busy_timeout`, and which is what made Step 4's read-then-write
allocator fail as `SQLITE_BUSY` on 43 of 50 concurrent writers.

A payment is the first document that genuinely needs to read: the split depends
on what the customer currently owes. The rule survives intact, because what it
actually requires is that the *first* statement be a write:

1. `UPDATE` — allocate the number, set the status, record who posted. The
   transaction is now a writer, and a second poster waits its turn on the busy
   timeout like any other.
2. Read the customer's balance.
3. Compute the split in Go, over `money.Money`.
4. Post the ledger entry.

Everything that looks like a precondition is checked after step 1, exactly as in
`advance.PostTx`: whether the payment exists, and whether it was still a
draft. Each returns an error, the caller rolls
back, and nothing happened.

### The split

With `payment.applies = interest-first`, and `owed` read at step 2 above:

```
interest applied  = min(amount, max(owed.Interest, 0))
principal applied = amount − interest applied
```

With `principal-first`, the two clamps swap. Three properties hold under either
rule and are tested as such:

- `principal applied + interest applied == amount`, always — the split divides
  the payment, it never changes its size.
- Neither part is negative.
- The part applied to a component never exceeds what was owed on that
  component, **except** where the whole payment exceeds the whole balance, in
  which case the excess lands on principal and the customer is in credit.

`max(owed, 0)` is not decoration: a customer already in credit has a negative
interest balance, and an unclamped `min` would apply a negative amount of
interest — that is, quietly charge them.

### What posting writes

One ledger entry. Not two, and not one per component: an entry carries both
deltas, and splitting them across two rows would make a payment look like two
events on a statement.

| Field | Value |
|---|---|
| `customer_id` | the payment's customer |
| `entry_date` | the payment's date |
| `entry_type` | `payment` |
| `principal_delta_cents` | the principal applied, **negative** |
| `interest_delta_cents` | the interest applied, **negative** |
| `source_table` | `payments` |
| `source_id` | the payment's id |
| `reason` | empty — a payment is not a correction |
| `created_by` | whoever posted |

Provenance points at the payment itself rather than at a line, because unlike a
voucher there is no row beneath the header. That is also why `source_id` is the
payment id and the domain package exports `SourceTable = "payments"`.

### What voiding writes

One reversal, the exact negation of the entry the payment produced, dated the
day of the void rather than the day of the payment — so a statement printed in
February still says in March what it said in February. The reversal carries the
void reason, and the balance returns to the centavo, including the interest the
payment had cleared.

Voiding is ManagerOnly. It is, together with voiding a voucher, one of the two
ways money leaves the record.

### Dates, amounts and methods

- `payment_date` is the business date, station-local, in the same shape as a
  ledger entry's. It defaults to today and a future date is refused at the edge
  — the January-slip-with-last-year's-date fault Step 5 guarded against cuts
  the same way here.
- `amount_cents > 0`, enforced by the schema. A negative payment is not a
  payment; money moving the other way is an advance or an adjustment.
- `method` is one of `cash`, `palay`, `other`, in a CHECK constraint.
- `particular` is free text and optional, the way a voucher line's is.

---

## The statement

A statement is a read over the ledger and nothing else. It belongs in
`internal/domain/ledger`, beside `StatementFor` and `BalanceAt` which already do
most of it, and it knows nothing about vouchers or payments.

```go
type StatementRow struct {
    Entry   Entry
    Running Balance   // the balance after this entry
}

type Statement struct {
    CustomerID int64
    From, To   Date
    Opening    Balance
    Rows       []StatementRow
    Closing    Balance
}
```

`Opening` is `BalanceAt(from − 1 day)`, or zero when `from` is zero. `Running`
accumulates in Go, over `money.Money`, so an overflow is an error rather than a
number that looks fine. `Closing` must equal `BalanceAt(to)` — that equality is
the statement's one real invariant and is tested over generated sequences.

**Document references are resolved above the ledger.** A statement row carries
`source_table` and `source_id`; turning those into "Ref. No. 1425" or "OR No. 7"
needs the documents, and the ledger is not allowed to know they exist. So the
API layer does it: a small resolver that takes a source table and a set of ids
and returns labels, implemented by `advance` (a line id → its voucher's Ref.
No.) and by `payment` (a payment id → its OR No.). `api` already imports both;
the ledger continues to import neither.

---

## Tasks

### 6.1 — The settings a payment needs

`internal/settings`: add `KindChoice` with an `Options []string` field on `Def`,
and validate membership in `Validate`. Then two settings:

| Key | Kind | Default | Doc |
|---|---|---|---|
| `payment.applies` | choice | `interest-first` | Which part of a debt a payment clears first. |
| `orno.start` | int | `1` | First OR No. this system issues. Set at cutover to continue the legacy sequence. |

Both appear on the settings API immediately, because `All` is the one list
seeding and the API both read.

**Done when:** `PUT /api/settings` refuses `payment.applies = "intrest-first"`
with a sentence naming the two legal values, and a fresh database seeds both.

### 6.2 — The payment schema

`internal/db/migrations/00005_payments.sql`, modelled on `00004_advances.sql`
with the line table removed:

- `payments` — id, `or_no` (NULL until posted, `CHECK (or_no IS NULL OR or_no > 0)`),
  `customer_id`, `payment_date`, `amount_cents > 0`, `method` in a CHECK list,
  `particular`, `status`, and the created/posted/voided actor trail with
  `void_reason`, including the same four status-implication CHECKs.
- A partial unique index on `or_no`.
- Indexes on `payment_date` and on `customer_id` — the second is what the
  statement and Step 8 walk.
- Triggers, keyed off `OLD.status` so they keep covering columns a later
  migration adds: status moves one way only; a posted payment is frozen; a
  posted payment cannot be deleted.

**Done when:** every trigger is proved by a test that tries the thing in raw
SQL — a `voided → draft` transition, an `UPDATE` of a posted row's amount, a
`DELETE` of a posted row — the way `advance/schema_test.go` does.

### 6.3 — The payment domain package

`internal/domain/payment`: `Payment`, `Draft`, `Status`, `Method`, `Service`,
`ValidationError`, and the CRUD half — create, read, update and delete a draft,
plus a `Query`/`Summary`/`Page` list matching `advance/list.go` (text over OR No.
and customer code or name, status filter, date range, the same pagination
bounds).

The package imports `ledger`, `money` and `settings`, and nothing imports it
except `api`. A draft carries no OR No. and no status, for the reason
`customer.Draft` carries no code and `advance.Draft` carries no Ref. No.: a rule
the type cannot express is a rule somebody has to remember.

**Done when:** a draft can be created, edited and deleted; a payment naming a
customer who is not on the books is refused with a sentence; a payment from a
*deactivated* customer is accepted (Decision 9); the list filters and
paginates.

### 6.4 — Posting, and the split, proved under concurrency

`PostTx`, in the write → read → compute → write order set out above, with the
split in its own pure function over `money.Money` and the setting:

```go
func split(amount money.Money, owed ledger.Balance, rule Rule) (principal, interest money.Money, err error)
```

Pure, so it can be tested exhaustively without a database, and so the property
tests can hammer it directly.

**Done when:** 20 concurrent posts under `-race` produce 20 distinct
consecutive OR Nos.; two simultaneous posts of one draft post it once; a payment
against a customer owing both principal and interest lands the split the rule
says; and a payment exceeding the balance leaves the customer in credit on
principal with interest at zero.

### 6.5 — Voiding

`VoidTx`: the status `UPDATE` first, then read what the payment posted through
`EntriesBySourceTx`, then reverse it. The same refusal as `advance.VoidTx` when
the document and the ledger disagree about what the payment did — one entry
expected, one entry found, or it refuses rather than voiding half of something.

**Done when:** voiding restores the balance to the centavo including cleared
interest; a second void is refused; voiding a draft is refused; an encoder
voiding is refused and the refusal is audited.

### 6.6 — The statement

`internal/domain/ledger/statement.go`: the `Statement` type above and
`(*Service).Statement(ctx, customerID, from, to)`.

`internal/api/statement.go`: the document-reference resolver, taking
`advance.Service` and `payment.Service` and mapping source rows to the numbers
staff say out loud.

**Done when:** `Closing` equals `BalanceAt(to)` over generated sequences; a
statement of a customer with no entries is an empty statement rather than an
error; reversals appear on it, because a statement that hides corrections is a
statement that lies.

### 6.7 — The API surface

| Route | Access | Why |
|---|---|---|
| `GET /api/payments` | Authenticated | the list everyone works from |
| `GET /api/payments/{id}` | Authenticated | |
| `POST /api/payments` | Authenticated | taking a payment is counter work |
| `PUT /api/payments/{id}` | Authenticated | editing a draft is the same work |
| `DELETE /api/payments/{id}` | Authenticated | a draft moved no money; its content survives in the audit row |
| `POST /api/payments/{id}/post` | Authenticated | |
| `POST /api/payments/{id}/void` | **ManagerOnly** | one of the two ways money leaves the record |
| `GET /api/customers/{id}/statement` | Authenticated | |

`or_no` and `status` supplied by a caller are refused at the edge, as `ref_no`
and `status` already are. Every mutating route goes through `rt.mutate`, so its
audit entry is written in the same transaction as the change.

**Done when:** the route coverage test passes with no new exceptions, and a
caller supplying `or_no` gets a 400.

### 6.8 — The screens

- `PaymentsPage.vue` — the list, newest first, with the status filter and date
  range the advances list already has.
- `PaymentNewPage.vue` / `PaymentPage.vue` — customer lookup, date, amount,
  method, particular; the balance shown live beside the amount; the overpayment
  warning; Post, and Void for a manager with its reason dialog.
- `StatementPage.vue` at `/customers/:id/statement` — date range, opening row,
  entries with running balance, closing row.
- `nav.ts` — "Payments" loses `soon`. "Statements" keeps it: there is no
  all-customers statement screen until Step 8.
- `CustomerPage.vue` — **Record payment** and **Statement** buttons beside the
  balance.

Entry is keyboard work, as at the voucher grid: customer, tab, amount, tab,
post. The counter PC runs Edge in `--app` mode at about 1024px and that is the
width this is checked at.

**Done when:** the everyday path walks in a browser against the real binary —
payment taken, saved as a draft, posted as OR No. 1, balance down by exactly the
amount, statement showing it with the right running balance, voided, balance
exactly back.

### 6.9 — Property tests, and the mutations that prove them

Invariants over generated sequences of advances, payments and voids:

1. A posted payment moves the balance down by exactly its amount.
2. `principal applied + interest applied == amount`, always.
3. Interest applied never exceeds interest owed, unless the payment exceeds the
   whole balance.
4. A voided payment restores the balance to the centavo.
5. A statement's closing balance equals `BalanceAt(to)`, for any sequence and
   any cut date.
6. OR Nos. are distinct, consecutive and gap-free.

**Two deliberate mutations**, each of which must make a named test fail:

- A read-then-write OR allocator → the concurrency test fails, and it should
  fail as `SQLITE_BUSY` rather than as a duplicate number.
- A split that clears interest without capping at the payment amount →
  invariant 2 fails.

---

## Verification

```bash
go test ./internal/domain/payment/... -race
go test ./internal/domain/ledger/... -race
go test ./... -run Property -count=10
make ci
```

Then the screens, in Chromium against the real binary with the embedded SPA,
signed in as a manager and again as an encoder, at 1024px, then swept from 700
to 1440px and at 390px. Nothing scrolls sideways; no console errors; every peso
figure grouped and signed the way the rest of the system prints them — the three
faults Step 5's walk turned up were all of that kind, and all three were
invisible until real money was on the screen.

---

## Risks

| Risk | Mitigation |
|---|---|
| A payment marked posted with no entry behind it | One transaction for status, entry and audit — the machinery from task 5.1 |
| Posting seizes up under two encoders | Write first, then read the balance; the order is stated in the code and tested at 20 writers |
| A double-clicked Post credits twice | `WHERE status = 'draft'`; zero rows is an error, not a retry |
| The split is wrong and nobody notices | It is a pure function with exhaustive tests and two property invariants over it |
| A customer in credit is quietly charged interest | `max(owed, 0)` before the `min`, with a test that starts from a credit balance |
| Q5 comes back as principal-first after payments exist | Nothing charges interest before Step 7, so no posted payment can be affected; the setting is changed and the rule applies from then on |
| The running balance is added up in the browser | Computed in Go and sent as strings, like every other peso figure |
| A statement disagrees with the customer page | Invariant 5: closing equals `BalanceAt(to)` |
| Someone edits a posted payment in a SQLite browser | Status-conditioned triggers in the schema |

---

## What this step deliberately does not do

- **No interest.** Step 7, blocked on Q1. The split rule is built and settable
  now; nothing exercises its interest half until there is an interest entry.
- **No per-voucher settlement or aging.** Decision 2. Step 8, derived rather
  than stored, once Q3 says what the report needs.
- **No OR slip printing, no A4 statement print CSS.** Steps 9 and 8.
- **No all-customers statement screen.** Step 8; the nav item stays disabled.
- **No delivery document.** Q2. A payment records a method and a peso amount.
- **No opening balances.** Step 10, as `opening` entries dated at the cutover.
- **No edit of a posted payment, and no un-post.** Void it and enter a new one.
