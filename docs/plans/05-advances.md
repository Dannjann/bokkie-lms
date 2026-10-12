# Step 5 — Advances

**Status:** ✅ built, gate passed · **Depends on:** Step 4 · **Blocks:** Steps 6, 8, 9, 10

---

## Goal

**The first thing in the system that moves money.** Steps 1–4 produced a binary
that installs, logs people in, backs itself up, can prove arbitrary ledger
arithmetic, and knows who the farmers are. Every balance in it is zero, because
nothing has ever been able to write a ledger entry.

This step builds the document that writes them: the advance voucher — the
legacy system's "Ref. No. 1425" header with its grid of lines beneath it —
entered as a draft, posted once, and undone only by reversal.

It is also the step where the ledger stops being a tested abstraction and
becomes the thing a farmer's balance is made of. Everything after it (payments,
interest, reports, slips, cutover) is a read over what this step writes, or
another writer alongside it.

## Done when

An encoder can enter a multi-line voucher at the counter, save it half-finished
and come back to it, and post it — after which the customers on those lines owe
exactly what the lines say, the voucher is numbered in the station's own Ref. No.
sequence, and nothing about it can be edited again. A manager can void it, after
which those same customers owe exactly what they owed before it was posted, to
the centavo, with both the advance and its reversal still on the record.

---

## Where this stands

All eight tasks are built and the gate has been run, screens included.

| Checked | Result |
|---|---|
| Five property invariants over generated sequences | ✅ hold |
| 20 concurrent posts under `-race` | ✅ 20 distinct consecutive Ref. Nos. |
| Two simultaneous posts of one draft | ✅ posted once |
| A voided three-line voucher across three customers | ✅ every balance exactly back |
| A second void, and voiding a draft | ✅ both refused |
| Editing or deleting a posted voucher | ✅ refused by the package, the API and the database |
| `UPDATE`/`DELETE` on a posted voucher's row, by hand in SQL | ✅ refused |
| A `voided → draft` transition, by hand in SQL | ✅ refused |
| A rolled-back post or void | ✅ no entries, no cache movement, still reversible |
| `refno.start` moved to 5000 after the sequence began | ✅ ignored, as designed |
| An encoder voiding | ✅ refused, refusal audited |
| A caller supplying `ref_no` or `status` | ✅ refused at the edge |
| **The screens, in a browser, at counter-PC resolution** | ✅ **see below** |

**Both mutations hold.** Each of the two tests carrying this step's weight was
checked against a deliberate break:

- A read-then-write Ref. No. allocator makes the concurrency test fail — and it
  fails exactly as Step 4 predicted, with `database is locked (SQLITE_BUSY)` on
  the ninth of twenty posters rather than with two vouchers sharing a number.
- A void that reverses only its first line fails the property test after 18
  generated sequences, shrunk to a two-line voucher for one centavo: *customer
  1 owes 1 centavos, want 0*.

**The screens were walked** in Chromium against the real binary with the real
embedded SPA, signed in as a manager and again as an encoder, at 1024px and
then swept from 700 to 1440px and at 390px. The everyday path holds: a
three-line voucher typed, saved as a draft, posted as Ref. No. 1, the farmers'
balances moved, voided with a reason, and the balances exactly back. An encoder
sees no Void button. Nothing scrolls sideways at any width, and no console
errors appear anywhere. Three faults it found are in *Decisions taken while
building* below — all three were invisible at any convenient width or on any
screen that had not had real money on it.

---

## Decisions taken while building

**1. The grid is not PrimeVue's cell-edit mode.** The plan called for an
inline-editing grid, and this is one, but its cells are ordinary
always-editable inputs. Entry here is keyboard work — a stack of slips, tab
from customer to amount to the next row — and a grid that needs a click to open
each cell doubles that. Enter on an amount starts the next line and puts the
cursor in it.

**2. The browser adds money up, in one place, in centavos.**
`00-architecture.md` says the frontend formats money and does not compute with
it. The running total under the grid is a deliberate exception: somebody
entering twelve lines needs to check the total against the paper *before*
saving, and the server's total does not exist until they do. It parses the text
into whole centavos with its own parser, never a float, and an unparseable
amount makes the total unknown rather than wrong — a half-typed figure must not
quietly count as nothing.

**3. Money from the server was printed ungrouped, and a reversal printed its
sign in the wrong place.** `₱1200.50` sat beside the grid's own `₱1,700.50`, and
a reversal read `₱-500.00`. `formatPesos` groups by string surgery — the value
never becomes a number — and puts the minus before the peso sign. The customer
screen's balance panel uses it too: a manager seeing pesos grouped on one screen
and not on another has a reason to distrust both.

**4. A one-pixel label dragged the page 316px wide.** The visually-hidden label
in the grid's last header cell is absolutely positioned, had nothing to be
positioned against, and so was laid out against the document — at the far edge
of a 46rem table inside a card that scrolls. It escaped the card and widened
the whole app at 390px. This step's equivalent of Step 4's 964–1032px band:
found by measuring overflow at the widths that are actually used, not by
looking at a screen.

**5. `advance.Actor` is the ledger's type, aliased.** The plan said to declare
it locally, as `internal/domain/customer` does. That reasoning does not carry
here: the customer package declares its own to *avoid* importing the ledger,
and this package already imports it in order to post entries. A separate type
would exist only to be converted back.

**6. Voiding checks the document and the ledger agree before undoing
anything.** If a voucher's line count and its posted entries ever disagree, the
void is refused rather than attempted. A partial void is worse than a refused
one, because it looks finished.

**7. `refno.start` reaches this package through the `Station` interface.** The
plan did not say where the setting comes from. It arrives the same way the
station's clock does — through a small interface `settings.Store` satisfies —
so that changing it takes effect immediately rather than at a restart, and so
tests can start the sequence where they like.

---

## Decisions settled before building

Seven questions were answered up front, because each changes what gets built
rather than how.

**1. Drafts live in the database, and posting is a separate act.** A voucher is
saved as `draft`, editable and reopenable tomorrow, and becomes real only when
somebody posts it. The alternative — the browser holds the lines until Post —
is simpler and loses a half-typed voucher to a closed tab, a flat battery or a
customer who walks off mid-transaction. At a counter that happens. The `draft`
status is already in `00-architecture.md` § Data model; this makes it mean
something.

A draft can be deleted outright. It never touched the ledger, so there is
nothing to reverse and nothing to preserve for a later dispute; what it said is
kept in the audit row's `before_json`, which is where a deleted draft should be
looked for.

**2. The Ref. No. is allocated at post, not at draft creation.** Numbering a
draft would mean an abandoned draft leaves a permanent hole in a sequence staff
read as a run — and at cutover the sequence continues the legacy one, so a gap
is a question somebody will ask. Allocating at post keeps the sequence
gap-free, and a draft is identified by its date and who started it until it has
a number of its own.

The cost is that a pre-numbered paper slip cannot be filled in alongside a
draft. That is the right trade here: the number on the paper should be the
number in the system, and the system's number should exist only once the
voucher does.

**3. A void undoes the whole voucher, never one line.** One wrong line means
voiding and re-entering, which leaves a legible trail — this voucher was
wrong, this one replaced it. Per-line reversal is kinder at the counter and
makes "what does this voucher currently total?" a question with a non-obvious
answer, which is exactly the kind of question that gets answered wrongly in
front of a farmer two seasons later.

**4. Each line names its own customer.** Q6 of the [domain
interview](domain-interview.md) — whether one Ref. No. genuinely spans several
farmers — is still unanswered, and this is the shape that is right either way:
it is what the schema and the legacy screen both say, and if the answer turns
out to be "one farmer per voucher" the grid is merely wider than it needed to
be. The other shape would need a migration and a rebuilt screen to recover
from a wrong guess.

The grid defaults a new row's customer to the one above it, so the
one-farmer-per-voucher case costs a single pick per voucher rather than one per
line.

**5. Encoders post; only a manager voids.** Entering an advance is the
counter's everyday work, the same as adding a farmer or fixing a phone number.
Voiding is the irreversible-ish act, and `00-architecture.md` § Security
already says a manager owns it. This mirrors the customer routes exactly, which
is what makes the rule explainable to the people it applies to.

**6. A voucher may be dated in the past, never in the future.** Yesterday's
slips get typed in this morning, so back-dating must work and there is no
window on it. A future date is almost always a wrong year on a January slip,
and it puts money into the ledger that has not moved yet — which then shows up
on a report as a balance nobody can account for.

**7. The customer screen is not touched.** Its balance panel was built in Step 4
against `ledger.BalanceFor` and starts showing real numbers the moment the first
voucher posts, without a line of change. A per-customer list of advances belongs
to Step 6, where payments have to interleave into the same list and the thing
worth building is a statement rather than a filtered table.

---

## Advance semantics

### Lifecycle

```
            post                void
draft ───────────────> posted ───────────> voided
  │
  └── delete (nothing to reverse; content kept in the audit row)
```

Three states and two transitions, and no others. There is no un-post, no
re-open, and no edit of a posted voucher: the correction path is void and
re-enter, which is the same discipline `ledger_entries` enforces one level
down.

Enforce it **in the database as well as in Go** — triggers that `RAISE` on any
`UPDATE` or `DELETE` of a voucher or a line once the voucher has left `draft`,
and on any status transition other than the two above. The Go service is the
only writer today, and will still be the only writer when somebody opens the
file in a SQLite browser at 9pm to "just fix one amount".

### Ref. No. allocation

`ref_no` is an `INTEGER` — the legacy numbers run into the 1400s and are read
and said aloud as plain numbers, so there is nothing to pad or prefix. It is
`NULL` on a draft and allocated at post, in the posting statement itself:

```sql
UPDATE advance_vouchers
   SET ref_no    = (SELECT COALESCE(MAX(ref_no), :start - 1) + 1 FROM advance_vouchers),
       status    = 'posted',
       posted_at = :now,
       posted_by = :user
 WHERE id = :id AND status = 'draft';
```

`:start` is the `refno.start` setting, which already exists and which Step 3's
settings work put there for this. `MAX` ignores the `NULL`s that drafts carry,
so the sequence counts posted vouchers only.

This is the same single-statement allocation as `BA-####` codes, for the same
reason and with the same backstop — a partial unique index on
`ref_no WHERE ref_no IS NOT NULL`. Step 4 found that a read-then-write allocator
fails as `SQLITE_BUSY` on 43 of 50 concurrent writers rather than as a duplicate,
which is much harder to attribute to its cause; there is no reason to rediscover
that here.

The `AND status = 'draft'` predicate is not decoration. It is what makes a
double-clicked Post button post once: the second statement matches no rows,
affects nothing, and the handler turns zero rows into "that voucher is not a
draft" rather than a second set of ledger entries.

> ⚠️ **`refno.start` only has an effect while no voucher has been posted.**
> Once `MAX(ref_no)` has a value it wins, by design — a setting that could
> renumber a live sequence is a footgun. At cutover the legacy number goes in
> before the first post. The settings screen should say so beside the field.

### Posting writes before it reads

The posting handler's **first statement inside the transaction must be that
`UPDATE`**, before it reads the voucher or its lines.

The database opens its transactions deferred. A transaction that reads first and
writes second has to upgrade its lock, and SQLite refuses an upgrade that would
deadlock **immediately**, without honouring `busy_timeout` — which is precisely
why Step 4's read-then-write mutation produced a storm of `SQLITE_BUSY` rather
than duplicate codes. Writing first makes the transaction a write transaction
from its first statement, and a concurrent poster then waits its turn on the
five-second busy timeout like any other writer.

Validation therefore happens *after* the write, inside the same transaction:
zero rows affected means it was not a draft; no lines means an empty voucher;
either way the handler returns an error, the transaction rolls back, and
nothing happened. This inverts the usual check-then-act order, and it is worth
a comment at the top of the function saying why, because it will otherwise look
like an accident and get "fixed".

Voiding has the same shape and the same reasoning: the status `UPDATE`
(`WHERE id = ? AND status = 'posted'`) goes first, which also makes a
double-clicked Void impossible before the ledger's at-most-once reversal index
ever gets involved.

### What posting writes

One `advance` ledger entry per line, all in the route's single transaction:

| Field | Value |
|---|---|
| `customer_id` | the line's customer |
| `entry_date` | the line's date |
| `entry_type` | `advance` |
| `principal_delta_cents` | the line's amount, positive |
| `interest_delta_cents` | 0 |
| `source_table` | `advance_lines` |
| `source_id` | the line's id |
| `created_by` | whoever posted |

Interest is zero here whatever the owner's answer to Q1 turns out to be. An
advance is principal; interest is a separate entry type written by a separate
run in Step 7, because that is what keeps "what did we lend?" and "what did we
charge?" separable on a statement.

Provenance points at the **line**, not the voucher. `idx_ledger_source` already
indexes `(source_table, source_id)`, so "which entries did this voucher
produce?" is a lookup over its line ids, and a line's entry is reachable from
the line itself rather than by matching amounts within a voucher.

### What voiding writes

One reversal per entry the voucher produced, in one transaction, plus the
voucher's status. `ledger.Reverse` already refuses a second reversal, refuses to
reverse a reversal, and dates the reversal today rather than back into a period
somebody has already been shown — none of that needs restating here, only
using.

A void carries a reason. The reason is stored on every reversal it writes, which
means the sentence typed once at the counter is on each entry that a later
statement will show.

**A voided voucher nets to exactly zero** for every customer it touched. That is
the invariant this step's property tests exist to hold, and it is the one a
farmer's dispute eventually rests on.

### Lines, dates and amounts

- **Amount must be greater than zero.** A negative advance is not an advance; a
  correction that moves money the other way is an `adjustment`, which is a
  manager's act with a reason attached, and it is not this document.
- **`line_date` defaults to the voucher's date** and stays editable. Both
  columns are in the schema already: a voucher typed on Tuesday can carry
  Monday's slips, and the ledger entry takes the line's date because that is the
  day the money moved.
- **A line's date is subject to the same rule as the voucher's** — any day up to
  today, never later. It need not match the voucher's date: a voucher dated
  Friday can carry a slip written on Wednesday, and the entry that line produces
  is the one a report for Wednesday has to find.
- **`particular`** is free text, bounded, and copied nowhere — it is what the
  legacy grid called it and what the farmer's slip says.
- **A voucher must have at least one line**, and at most a bounded number of
  them (200 is far past the largest plausible day and stops a malformed request
  writing a hundred thousand ledger entries).
- A customer may appear on **several lines of the same voucher**. Two advances to
  the same farmer on the same day are two rows on the paper and two entries in
  the ledger, and netting them would lose the particular that distinguishes them.
- **Deactivated customers cannot be added to a draft**, but a draft that already
  names one can still be posted. Blocking the post would strand a voucher that
  is correct, because deactivation is about who is on the books today rather
  than about who owed money last week.

---

## Tasks

### 5.1 — Transaction-aware ledger writes

**The seam that has to close before anything else in this step is safe.**

`ledger.PostBatch` and `ledger.Reverse` each open their own transaction
(`s.inTx`). The route wrapper `mutate` opens one too, and writes the audit row
inside it. Posting a voucher therefore spans two transactions today: the
voucher's status in one, its ledger entries in another. A crash between them
leaves a voucher marked posted with no entries behind it — in the one table that
cannot be repaired by editing.

Add transaction-taking variants, with the existing methods becoming thin
wrappers around them:

```go
func (s *Service) PostBatchTx(ctx context.Context, tx *sql.Tx, entries []Entry) ([]int64, error)
func (s *Service) ReverseTx(ctx context.Context, tx *sql.Tx, entryID int64, by Actor, reason string) (int64, error)
func (s *Service) EntriesBySourceTx(ctx context.Context, tx *sql.Tx, table string, ids []int64) ([]Entry, error)
func (s *Service) EntriesBySource(ctx context.Context, table string, ids []int64) ([]Entry, error)
```

`insert` and `entryTx` already take a `*sql.Tx`, so `PostBatch` becomes
`inTx` + `PostBatchTx` and the posting path loses its duplicate. The cache
refresh stays where it is, inside the same transaction as the entries, which is
the property Step 3's first invariant depends on.

`EntriesBySource` is new API surface, and it is what a void reads to find what
it must undo and what the posted-voucher screen shows. It belongs to the ledger
rather than to a join written in the advance package, because "what entries
came from this document?" is a question about entries.

**Done when:** every existing ledger test still passes unchanged, and a voucher
post and a void each run entirely inside one caller-supplied transaction — proved
by a test that rolls that transaction back and finds no entries, no cache
movement and no status change.

---

### 5.2 — The advance schema

`00004_advances.sql`, migration only. No handlers, no API.

```
advance_vouchers
  id, ref_no (unique, null while draft), voucher_date, notes,
  status (draft|posted|voided),
  created_at, created_by, posted_at, posted_by, voided_at, voided_by, void_reason

advance_lines
  id, voucher_id, customer_id, line_date, particular, amount_cents
```

- `CHECK` on `status`, and on the fields each status implies: a posted voucher
  has a `ref_no` and a `posted_at`; a draft has neither; a voided voucher has a
  reason.
- `CHECK (amount_cents > 0)`.
- Partial unique index on `ref_no WHERE ref_no IS NOT NULL`.
- Indexes on `advance_lines(voucher_id)` and `advance_lines(customer_id)` — the
  second is what Step 6's statement and Step 8's reports will walk.
- Triggers refusing `UPDATE` and `DELETE` on both tables once the voucher has
  left `draft`, and refusing any status transition other than
  `draft → posted` and `posted → voided`.

The triggers here are narrower than `ledger_entries`' total ones, and that is
unavoidable: a draft is meant to be edited. The line the trigger draws is the
status, so the condition is on `OLD.status` rather than on a list of columns,
and it keeps covering columns a later migration adds.

**Done when:** migrations apply cleanly; an `UPDATE` of a posted voucher's line,
a `DELETE` of a posted voucher, and a `voided → draft` transition are each
refused by the database with no Go involved.

---

### 5.3 — The advance domain package

`internal/domain/advance`, following `internal/domain/customer`'s shape: a
`queryExecer` so the same code serves `*sql.DB` and the API's `*sql.Tx`,
validation and normalisation in the package rather than the handlers, and a
locally declared `Actor`.

```go
type Voucher struct {
    ID          int64
    RefNo       int64      // 0 while draft
    Date        ledger.Date
    Notes       string
    Status      Status     // draft | posted | voided
    Lines       []Line
    Total       money.Money
    CreatedAt   time.Time
    CreatedBy   Actor
    PostedAt    time.Time
    PostedBy    Actor
    VoidedAt    time.Time
    VoidedBy    Actor
    VoidReason  string
}

type Line struct {
    ID         int64
    CustomerID int64
    Code, Name string      // joined for display; never written from here
    Date       ledger.Date
    Particular string
    Amount     money.Money
}

// Draft is everything a caller may decide. No RefNo and no Status, for the same
// reason customer.Draft has no Code: a rule the type cannot express is a rule
// somebody has to remember, and decodeJSON already refuses unknown fields.
type Draft struct {
    Date       ledger.Date
    Notes      string
    Lines      []DraftLine
}
```

Unlike `customer`, this package **does** import `internal/domain/ledger`, and
that is the correct direction: an advance is a document that produces ledger
entries, so it has to know the ledger exists. The ledger still knows nothing
about vouchers — it takes entries with a `source_table` string and never asks
what is in that table. The dependency points one way, and Step 3's boundary
holds.

Service methods:

```go
Create(ctx, q, Draft, Actor) (Voucher, error)      // a draft
Update(ctx, q, id, Draft, Actor) (Voucher, error)  // a draft; replaces its lines
Delete(ctx, q, id, Actor) error                    // a draft only
Post(ctx, tx, id, Actor) (Voucher, error)
Void(ctx, tx, id, Actor, reason string) (Voucher, error)
ByID(ctx, q, id) (Voucher, error)
List(ctx, q, Query) (Page, error)
```

`Update` replaces a draft's lines wholesale rather than diffing them. A draft
has no downstream references — no ledger entry points at a draft's line — so
there is nothing a delete-and-reinsert can orphan, and the alternative is a
diffing path that exists only to save writes on a table with a dozen rows.

**Done when:** the package creates, edits, deletes, posts and voids against an
in-memory database with no HTTP layer involved; editing a posted voucher is
refused by the package *and* by the database; and a draft with no lines cannot
be posted.

---

### 5.4 — Posting, proved under concurrency

The allocation statement above, the write-before-read ordering, and the
all-or-nothing guarantee that `PostBatchTx` gives for free.

N encoders posting different drafts at the same moment must get N distinct
Ref. Nos. with no failures, under `-race`. **Check the test can fail**: swap the
allocator for a read-then-write and confirm it does — and record *how* it fails,
because Step 4's evidence says the symptom will be `SQLITE_BUSY` rather than a
duplicate number, and the next person should not have to rediscover that.

Separately: posting the same draft twice concurrently must post it exactly once,
with the loser told it is not a draft.

**Done when:** 20 concurrent posts produce 20 distinct consecutive Ref. Nos.
under `-race`; a doubly-posted draft yields one voucher and one set of entries;
and both mutations are caught.

---

### 5.5 — Voiding

Status first, then the entries, then the reversals — all in the route's
transaction.

- Every entry the voucher produced is reversed, or none is.
- The reason is required and lands on each reversal.
- Voiding a draft is refused (delete it), and voiding a voided voucher is
  refused by the status predicate before the ledger's index is reached.
- After the void, `BalanceFor` returns exactly what it returned before the post.
  Not within a centavo — exactly.

**Done when:** a voided three-line voucher across three customers leaves all
three balances precisely where they started, the voucher and its reversals are
all still on the record, and a second void changes nothing.

---

### 5.6 — The API surface

| Route | Access | Why |
|---|---|---|
| `GET /api/advances` | Authenticated | the list everyone works from |
| `GET /api/advances/{id}` | Authenticated | |
| `POST /api/advances` | Authenticated | starting a voucher is counter work |
| `PUT /api/advances/{id}` | Authenticated | editing a draft is the same work |
| `DELETE /api/advances/{id}` | Authenticated | a draft moved no money; its content survives in the audit row |
| `POST /api/advances/{id}/post` | Authenticated | entering advances is the counter's job |
| `POST /api/advances/{id}/void` | **ManagerOnly** | the only way money leaves the record |

All seven are `mutate` — transactional, audited in the same transaction. Nothing
here needs the self-audited exception: unlike the customer import, every one of
these is one event with one outcome.

Actions: `advance.draft.created`, `advance.draft.updated`,
`advance.draft.deleted`, `advance.posted`, `advance.voided`; entity `advance`.

The list response carries each voucher's total and its customers' names, both
computed server-side — `SUM(amount_cents)` is exact on integers, and no peso
figure is ever added up in JavaScript.

The detail response for a posted voucher carries the ledger entries it produced,
composed in the API layer from `advance.ByID` and `ledger.EntriesBySource` — the
same composition the customer detail already uses for its balance.

**Done when:** every route's access level and audit style is asserted by the
route table test, and an encoder attempting a void is refused with the refusal
in `audit_log`.

---

### 5.7 — The screens

- `/advances` — voucher list: Ref. No. (or "Draft"), date, customer or customer
  count, total, status. Status filter defaulting to **all**, because an
  unfinished draft is something somebody needs to see. Ordered by date then id,
  newest first.
- `/advances/new` and `/advances/:id` — the voucher form with the inline-editing
  line grid. Customer picker per row against `GET /api/customers`, which Step 4
  built and documented as this screen's query. Save draft, Post, and — on a
  draft — Delete.
- A posted voucher renders read-only, showing the entries it produced. A manager
  additionally sees Void, which asks for a reason and will not proceed without
  one.
- `nav.ts`: Advances loses `soon` and gains `to: '/advances'`. Nothing else in
  the rail changes, which is what that file was built for.

**The grid is the one genuinely unusual screen in the system** and the reason
`00-architecture.md` chose PrimeVue at all. Entry is keyboard work: a row is
added without reaching for the mouse, tabbing off the last field starts the next
row, and the amount column is right-aligned and typed as digits. Somebody
entering twenty lines from a stack of slips should never touch the pointer.

Step 4 deliberately did not build a reusable list/detail abstraction, on the
grounds that it could not be designed from one example. This is the second
example. Generalise only what both screens genuinely share — the page header
already is that, and it may be all of it.

**Done when:** a voucher can be entered, saved as a draft, reopened, posted and
voided from a browser; the customer's balance panel shows the advance
immediately and returns to its previous figure after the void; and the grid works
in an `--app` window at counter-PC resolution, including the 964–1032px band
Step 4 found the hard way.

---

### 5.8 — Property tests, and the mutations that prove them

In the house style, generating random sequences of create, edit, delete, post and
void across several customers and asserting:

1. **A posted voucher's lines equal its entries.** The sum of a voucher's line
   amounts equals the sum of the principal deltas of the entries naming its
   lines, for every posted voucher, always.
2. **A voided voucher nets to zero.** Every customer it touched ends exactly
   where they started.
3. **Drafts move no money.** Any number of drafts created, edited and deleted
   leaves every balance untouched.
4. **Ref. Nos. are unique, consecutive from `refno.start`, and never reused** —
   not after a delete, not after a void.
5. **Derived == rebuilt still holds.** Step 3's first invariant, now over a
   ledger written by real documents rather than by a generator.

Two mutations, each of which must break something:

- **A void that reverses only the first line** — invariant 2 fails.
- **A read-then-write Ref. No. allocator** — the concurrency test in 5.4 fails.

A property test that has never been seen to fail is reporting on nothing, and
these two are the ones carrying this step's weight.

**Done when:** all five invariants hold over thousands of generated sequences,
and both mutations are caught.

---

## Verification

```bash
go test ./internal/domain/advance/... -race
go test ./internal/domain/ledger/... -race
go test ./... -run Property -count=10
make ci
```

- A posted voucher's entries sum to its lines, over thousands of sequences.
- A voided voucher returns every balance to exactly its prior value.
- Drafts move no money; deleted drafts leave the audit row behind.
- 20 concurrent posts produce 20 distinct consecutive Ref. Nos. under `-race`.
- A doubly-posted draft posts once; a doubly-voided voucher voids once.
- An encoder cannot void, and the attempt is audited.
- Editing a posted voucher is refused by the database, not only by Go.

Then, by hand, against the real binary at ~1024px: enter a three-line voucher
across three customers, save it as a draft, reload, post it, check all three
customers' balances, void it as a manager, and check them again.

## Risks

| Risk | Mitigation |
|---|---|
| A voucher marked posted with no entries behind it | One transaction for status, entries and audit — 5.1 exists for this |
| Two encoders get the same Ref. No. | Allocation inside the `UPDATE`; partial unique index behind it; concurrency test |
| Posting seizes up under two encoders | Write before read, so no deferred transaction ever upgrades its lock |
| A double-clicked Post posts twice | `WHERE status = 'draft'`; zero rows is an error, not a retry |
| A void undoes part of a voucher | One transaction; property invariant 2; the mutation test for it |
| Someone edits a posted voucher in a SQLite browser | Status-conditioned triggers in the schema |
| `refno.start` set after the first post and silently ignored | Documented here, said on the settings screen, and set at cutover |
| A wrong year on a January slip | Future dates refused at the edge |
| The grid is slow to type into | Keyboard-first entry is a gate item, checked at counter-PC resolution |

---

## What this step deliberately does not do

- **No slip printing.** `POST /api/vouchers/{id}/print-slip` is Step 9, along
  with the ESC/P templates and the alignment settings.
- **No payments, no allocation, no statement.** Step 6. Until it exists, a
  balance only ever goes up.
- **No interest.** Step 7, and blocked on Q1 of the domain interview. Advance
  entries carry a zero interest delta whatever that answer turns out to be.
- **No per-line void, no un-post, no edit of a posted voucher.** Decision 3.
- **No opening balances.** Step 10, as `opening` entries dated at the cutover.
- **No reports.** Step 8, blocked on Q3/Q4.
- **No per-customer advance list.** Decision 7 — it becomes a statement in
  Step 6, and building a filtered table now means rebuilding it then.
