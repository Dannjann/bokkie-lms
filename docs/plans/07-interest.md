# Step 7 — Interest

**Status:** ⛔ superseded by [07.5-settlement.md](07.5-settlement.md) · **Depends on:** Step 6 · **Blocks:** Steps 8, 10

> **Superseded, 2026-10-15.** Reading the legacy program answered Q1 from its
> code and data ([legacy-findings.md](legacy-findings.md)): the station settles
> one farmer at a time, charging each advance by the day at a monthly rate
> chosen on the settlement. That is exactly the case below calls "would still
> cost code", so the interest run described here was removed and replaced by the
> settlement. This page is kept as the record of what was built and why.

> **Q1 of the [domain interview](domain-interview.md) is unanswered**, and this
> step is built anyway. That is a decision, not an oversight, and *Building on
> an unanswered question* below is the whole of the reasoning. The short form:
> the rate and the rule become settings, the default charges nothing, and the
> step ships a machine that is right under any of the likely answers.

---

## Goal

**The first thing in the system that makes a balance go up without anybody
lending anything.** Until now every centavo in the ledger came from a document
somebody typed: an advance out, a payment back. Interest is different — it is
the station charging for money already lent, computed from the record rather
than read off a slip, against every farmer at once.

This is the legacy system's "Compute Percentage" button, and it is the most
dangerous button in the application. An advance posted wrong is one farmer's
voucher and one conversation. An interest run posted wrong is three hundred and
sixty-four farmers' balances, all slightly wrong, all plausible, and nobody
notices until somebody disputes one.

So the shape of this step is not really about arithmetic. It is about making
the dangerous act previewable, reversible, and impossible to do twice.

## Done when

A manager can open Interest, choose a period, and see exactly what a run would
charge — every farmer, the balance it was computed from, and the peso figure —
before anything is written. Posting it turns that preview into one ledger entry
per line, in one transaction, numbered from the station's own run sequence.
Posting the same period twice is refused. Voiding the run returns every one of
those balances to the centavo. And a payment taken afterwards finally divides
between interest and principal, which is the thing `payment.applies` was put in
for in Step 6 and has had nothing to bite on since.

---

## Where this stands

All nine tasks are built and the gate has been run, screens included.

| Checked | Result |
|---|---|
| Charge against the rounding boundaries, both directions | ✅ half-up, and half-even would differ |
| Eight property invariants over generated ledgers | ✅ hold |
| `BalancesAt` against `BalanceAt`, per customer, at any date | ✅ cannot disagree |
| An advance voided after the period ended | ✅ excluded from a flat charge |
| 20 concurrent posts of 20 runs | ✅ 20 consecutive numbers, no gaps |
| 20 simultaneous posts of one run | ✅ charged once |
| The same period twice | ✅ refused, naming the run that covers it |
| A period overlapping a posted one by a fortnight | ✅ refused, nothing written |
| A payment between the preview and the post | ✅ refused, naming the farmer |
| A new borrower between the preview and the post | ✅ refused |
| A run with nothing to charge | ✅ refused, still a draft |
| A rolled-back post | ✅ no entry, no cache movement, still a draft |
| Voiding | ✅ every balance back to the centavo |
| Voiding twice, and voiding a draft | ✅ both refused |
| Voiding, then charging the period again | ✅ allowed, and numbered 2 |
| Editing or deleting a posted run, by hand in SQL | ✅ refused |
| Editing a posted run's rate, by hand in SQL | ✅ refused |
| Two posted runs over one period, by hand in SQL | ✅ refused by the index |
| A line charging nothing, or one farmer twice | ✅ refused |
| A customer in credit | ✅ not charged |
| An encoder on any of the seven routes | ✅ refused, all seven refusals audited |
| A caller supplying lines, a number, a status or a rule | ✅ refused at the edge |
| A payment after a run, under both `payment.applies` values | ✅ divides as the setting says |
| **The screens, in a browser, at counter-PC resolution** | ✅ **see below** |

**All four mutations hold**, and each was checked against a deliberate break:

- A read-then-write `run_no` allocator fails the concurrency test exactly as
  Step 4 predicted — `database is locked (SQLITE_BUSY)` on the first of twenty
  posters, not two runs sharing a number.
- Rounding down instead of half-up fails three rows of the table test, including
  the half-centavo case that also separates half-up from half-even.
- A void that reverses only its first line leaves the second farmer charged —
  caught by the example test *and*, independently, by the property test, which
  shrank it to a one-centavo counterexample.
- Removing the period guard charges September twice over. It is worth recording
  what the two halves of that guard did separately when it was gone: the
  *overlapping* period went through silently, and the *identical* period was
  refused by the unique index as `UNIQUE constraint failed:
  interest_runs.period_from, interest_runs.period_to`. Neither half covers the
  other.

**The screens were walked** in Chromium against the real binary with the real
embedded SPA, signed in as a manager and again as an encoder, at 1024px, then
swept from 700 to 1440px and at 390. Two farmers owing ₱5,000.00 and ₱1,234.56;
a 3% run previewing ₱150.00 and ₱37.04; posted as Interest run 1; the charge on
the farmer's statement under that name; the same period refused afterwards; the
run voided, and both balances exactly back. A third farmer, who owes nothing,
never appears. Nothing scrolls sideways at any width, and the only console error
anywhere is the 409 the refused post is supposed to produce.

---

## Decisions and faults from building

**1. The period guard belongs in the posting `UPDATE`, not in a check after
it.** The plan had it as step 3 of five, a query run after the write — and that
is wrong in a way only the API test exposed. A run over *exactly* the same dates
trips the unique index inside the `UPDATE` itself, so the transaction failed
with `UNIQUE constraint failed: interest_runs.period_from…` and the handler
turned it into a 500 before the check that would have named the run ever ran.
The condition now lives in the statement's `WHERE` clause. Zero rows affected
means one of three things, each with its own sentence: no such run, not a draft,
or the period is taken — and the overlap query survives only to name the run
that covers it.

**2. The preview is compared positionally, not by lookup.** Both sides come back
ordered by customer id, so `stale` walks them together; a farmer appearing or
disappearing is caught at the position they would have occupied. A map lookup
would have needed a second pass to notice the appearing case at all.

**3. The rule panel lives on the interest screen, because there is no settings
screen.** "Settings" still reads SOON in the rail. A refusal that says *choose
how interest is computed in Settings* would have sent a manager somewhere that
does not exist, so the three keys are edited on the interest list itself,
through the ordinary settings API. When the settings screen does arrive it edits
the same values and neither has to be unwound.

**4. A percentage becomes basis points through `parseCentavos`.** Not a borrowed
parser: a percentage with at most two decimal places and an integer of
hundredths are the same thing, so "3" is 300, "2.5" is 250 and "0.25" is 25,
with nothing passing through a float. The server is still the one that decides
what was typed; this only decides what to send.

**5. A label pointing at nothing.** PrimeVue puts the `id` given to
`DatePicker`, `Select` and `AutoComplete` on the wrapper element rather than on
the input inside it, so every `<label for>` on these forms addressed a `<span>`:
clicking the label focused nothing, and a screen reader had no association to
read. `input-id` is the prop that does what was meant. This was not new — Step
6's payment form and the statement's date pickers had it too — and all nine
sites are fixed.

**6. The Rule column wrapped onto three lines** at 1024px, which buried the
rate, the one figure the column exists for. The basis is now named only when it
is `principal-and-interest`, the case that changes what money does; annotating
every ordinary row with "principal only" cost more than it said.

**7. The refusal said "already" twice** — *"that period has already been
charged: interest run 2 already covers…"*. It is the sentence a manager reads at
the moment they are stopped, and it now reads once.

**8. A blank screen after an update, found by walking the new screens.** Every
route loads its page with a dynamic import, so each screen is a file with a
content hash in its name. A browser open since before a new build asks for a
file that is no longer there, the import rejects, and vue-router stops: on a
click the navigation aborts and the old screen stays, and on a refresh or a
typed address there is no old screen, so the rail and topbar render around an
empty middle.

**That is what `bokkie update` does to a counter PC.** The station runs the app
in a chromeless window nobody closes for weeks; a USB update swaps the binary
underneath it, and the next screen anybody opens is a chunk that no longer
exists. It was found here because rebuilding the SPA several times in an
afternoon does the same thing to an open tab — but it is Step 2.5's failure
mode, not this step's, and it would have arrived at the station on the first
update.

`router.onError` now recognises the three engines' wordings for a failed module
import and does a full load of the address that was asked for, which fetches an
`index.html` naming the files that do exist. Once per destination, remembered in
`sessionStorage` so a chunk that is genuinely missing cannot become a browser
that reloads itself for ever. Four cases are checked in the browser: a refresh
recovers, a click lands on the page that was asked for rather than the one being
left, a permanently missing chunk reloads exactly once, and a healthy session
never reloads at all.

**9. A run that charges nobody blanked its own screen.** Reported from the
station's own database and reproduced exactly: Go marshals a nil slice as
`null`, a run whose period has no balances in it has no lines, and the screen
reading `run.lines.length` off a `null` threw — which in Vue takes the whole
component down and leaves the shell wrapped around an empty middle. The
breadcrumb still said *Draft interest run*, which is what made it look like a
routing fault rather than a render one.

Every walk-through had produced runs with lines on them, so the gate never saw
it. The fix is at the source rather than in the template: a collection on the
wire is a list, empty or not. `linesOf` returns `[]Line{}`, the identical shape
in `advance.linesOf` is fixed alongside it — a voucher cannot have zero lines
today, so it had never fired — and the backup list, which no screen reads yet,
is fixed before one does. The test asserts on the raw response body, because
decoding is exactly what hides the difference: `null` and `[]` both unmarshal
into a nil slice in Go, and the browser is where they stop being the same.

**10. `payment.applies` is finally exercised against real interest.** Step 6 had
to write an interest entry by hand to see its own split work. The test that
belongs to this step posts a run and then a payment: ₱1,000.00 against ₱5,000.00
of principal and ₱150.00 of interest divides 850/150 under interest-first and
1,000/0 under principal-first.

---

## Building on an unanswered question

Q1 asks how "Compute Percentage" actually works: flat percentage once at
release, monthly on the outstanding balance, or per cropping season; what the
rate is; whether it compounds; whether it is ever waived; what triggers a run;
and what happens to interest when a farmer repays early. It is unanswered, and
the interview it belongs to has not happened.

The reason to build anyway is that **almost none of that is structure.** Under
every likely answer, the same things are true:

- interest is a charge against a customer, computed from the ledger, written as
  a `ledger.TypeInterest` entry — an entry type Step 3 already defined, already
  validates, and already refuses to let carry principal;
- a person must be able to see what will be charged before it is charged;
- it must be undoable in one act, because the failure mode is hundreds of rows
  at a time;
- and it must refuse to charge the same period twice.

What the answer actually decides is one pure function of two numbers. So the
function is named, given a small closed set of implementations, and **chosen by
a setting whose default charges nothing at all.** A fresh install computes no
interest until somebody deliberately sets a rate — the same posture
`payment.applies` took in Step 6, and for the same reason: a default that
silently does something to money is worse than one that does nothing.

**What the answer can still cost.** Two shapes are outside this design, and
both would be code rather than a settings change. They are recorded here so that
"we thought about it" is checkable rather than claimed:

- **Pro-rating on early repayment.** If interest is charged for a period and a
  farmer settles halfway through it, a pro-rated refund is a *negative* interest
  movement. It is expressible — an adjustment entry, or a reversal and a fresh
  run — but nothing here computes it.
- **A rate negotiated per customer.** Every rule here is station-wide. A
  per-customer rate means a column on `customers` and a rate resolved per line,
  which is a migration and a change to `Charge`'s signature.

If Q1 comes back as either of those, this step is extended, not thrown away:
the run, the preview, the numbering and the void are all still right.

---

## Decisions settled before building

**1. Interest is a run, and a run is a document.** `interest_runs` with
`interest_run_lines` beneath it, shaped like the advance voucher and its grid:
draft → posted → voided, one way, with the same triggers refusing an edit after
posting. It is a document and not a background job because somebody has to be
accountable for it — a row that says who charged what, when, and at what rate,
that a manager can be shown six months later.

**2. The draft is the preview.** Not a description of what a run would do: the
run itself, minus the commit. This is Step 4's CSV import rule restated, and it
is restated because it is the rule that makes a preview trustworthy. A
"preview" computed by different code than the thing it previews is a second
implementation that will eventually disagree with the first, and it will
disagree in the direction nobody checks.

So creating a draft computes every line and stores it. The screen reads rows
from the database. Posting writes those rows to the ledger.

**3. Posting recomputes, and refuses to post a stale preview.** The one thing
the draft cannot promise is that the world stood still. A farmer can pay between
the preview and the post, which changes the balance a monthly charge is computed
from.

Posting therefore recomputes every line inside the posting transaction and
compares. If any line differs, **the post is refused**, naming the customer and
both figures — *"BA-0042 owed ₱5,000.00 when this run was previewed and owes
₱3,000.00 now; refresh the run"*. Refreshing is one click.

The alternative — post the fresh number — was rejected deliberately. A manager
approved a specific set of figures. Substituting different ones silently, at the
moment of writing three hundred entries, is exactly the class of event this step
exists to make impossible.

**4. The rule is a pure function, and the rate is in basis points.**

```go
func Charge(base money.Money, rule Rule) (money.Money, error)
```

Pure, exported, no database, hammered directly by the property tests. The rate
is an integer in basis points — 300 is 3%, 25 is a quarter of a percent —
because a percentage stored as a decimal string is a float waiting to happen,
and `internal/domain/money` has a test that parses the syntax tree and fails on
one.

The arithmetic is `money.MulDiv(rateBps, 10_000, RoundHalfUp)`, which is the
method `money.go` was written anticipating this step — full-width multiply,
then one rounding of the exact quotient, never the rounding of an already
rounded step.

**5. Rounding is half-up, per customer, and it is a decision rather than a
default.** `money.Rounding` has no zero value for exactly this reason. Half-up
is what a person does with a pencil, and every one of these figures may be
checked by a farmer with a pencil. Rounding happens once per line — there is no
aggregate to round, because each customer's charge is its own entry.

**6. The rule is snapshotted onto the run.** `method`, `rate_bps` and `basis`
are columns on `interest_runs`, not merely settings that were read at the time.
A run opened in two years has to be able to say what produced it, and by then
the setting will have moved. This is the same instinct as copying a void reason
onto the reversal instead of joining back for it.

**7. Three methods, one of which does nothing.**

| `interest.method` | What it charges |
|---|---|
| `none` *(default)* | Nothing. Creating a run is refused while this is set. |
| `monthly-on-balance` | The rate against what each farmer owed at the end of the period. |
| `flat-on-advance` | The rate against what each farmer was advanced *during* the period, once. |

`none` is the default so that an install which has never been configured
charges nothing, and so that the answer to Q1 arriving as "they don't actually
charge interest, that button was never used" costs a settings screen and no
code at all.

`interest.basis` decides what `monthly-on-balance` means by "owed": `principal`
(simple interest, the default) or `principal-and-interest` (compounding —
interest charged on unpaid interest). That distinction is a sub-question of Q1,
it changes what money does, and it is a choice setting rather than a boolean so
that the screen has to say which one it means.

**8. A period, not a date.** A run carries `period_from` and `period_to` rather
than a single "as of" day. Both methods need the pair: `monthly-on-balance` uses
`period_to` as the day balances are read at, and `flat-on-advance` needs the
range to know which advances it covers. Carrying both from the start also means
Q1 answering "per cropping season" is a different pair of dates rather than a
different schema.

**9. The same period is refused twice.** The guard is two things, and it needs
both:

- a unique index over `(period_from, period_to)` among *posted* runs, which
  catches the ordinary case — somebody clicking Post on two drafts of the same
  month;
- an overlap check in Go, inside the posting transaction after its first write,
  because an index cannot express "these ranges intersect". A posted run for
  September refuses a post of 15 Sept – 15 Oct.

Voiding frees the period, which is right: a run voided because the rate was
wrong must be re-runnable.

**10. Nobody is charged twice inside one run either.** `UNIQUE (run_id,
customer_id)` on the line table. A run is one row per farmer, and the schema
says so rather than the code remembering to.

**11. A line that charges nothing is not a line.** Customers whose base is zero
or negative — nothing owed, or already in credit — produce no row. The run
charges who it charges, and a preview padded with three hundred zero rows hides
the twelve that matter. It follows that a run with no lines at all cannot be
posted: there is nothing to write, and a posted run that wrote no entries is a
document that says something happened when nothing did.

**12. The whole section is ManagerOnly, reads included.** Every other document
in the system opens its everyday half to encoders, because a farmer is standing
at the counter and the work is theirs. Nothing about an interest run is counter
work: nobody is standing there, it is not typed from a slip, and it changes what
every farmer owes. Reading is held back with the rest because the preview *is*
the dangerous artifact — it is the screen that ends in a Post button.

**13. The ledger gains two bulk reads, and learns nothing.** `BalancesAt` (every
customer's balance as at a date) and `AdvancedBetween` (principal advanced per
customer over a period, net of the reversals that undid it). Both are sums over
entries, dates and entry types, which are the ledger's own vocabulary — it is
`TypeAdvance` that is being summed, not a voucher. The ledger still does not
know that `advance_vouchers` exists, and this step does not teach it.

The alternative was computing bases in the interest package with its own SQL
over `ledger_entries`, which would put a second query against the ledger's table
outside the ledger — the thing Step 3 exists to prevent.

**14. `payment.applies` finally means something.** Step 6 shipped the setting
with nothing to exercise it: every payment before this step was entirely
principal whatever the rule said. The tests that prove interest-first and
principal-first actually divide a real payment belong to this step, because this
is the step that makes them possible.

---

## Interest semantics

### Lifecycle

```
draft ──post──> posted ──void──> voided
```

The voucher's three states and two transitions, with no un-post and no re-open.
A draft can be recomputed, edited into a different period, or deleted outright;
it has written nothing. A posted run is as fixed as the entries beneath it. A
voided run and its reversals both stay on the record.

### Run numbering

`run_no` is NULL until posted, allocated inside the posting `UPDATE` from
`COALESCE(MAX(run_no), 0) + 1`, with a partial unique index behind it — the
`ref_no`/`or_no` arrangement exactly.

**No `*.start` setting.** Ref. No. and OR No. each continue a legacy sequence
staff read off paper; there is no legacy run sequence to continue, and a setting
nobody needs is a setting somebody can get wrong.

### What a run computes

For each customer, a **base** and a **charge**:

| Method | Base |
|---|---|
| `monthly-on-balance`, basis `principal` | `BalanceAt(period_to).Principal` |
| `monthly-on-balance`, basis `principal-and-interest` | `BalanceAt(period_to).Principal + .Interest` |
| `flat-on-advance` | principal advanced between `period_from` and `period_to`, net of reversals |

```
charge = base × rate_bps ÷ 10 000, rounded half-up
```

Bases that are zero or negative are skipped, not charged. Charges that round to
zero — a farmer owing ₱1.00 at 3% — are skipped too: an entry that moves no
money is refused by `ledger.Entry.Validate` anyway, and a line the ledger would
refuse has no business being previewed as if it would post.

**Reversals in `flat-on-advance`.** An advance voided after it was made must not
be charged interest. A reversal is dated the day of the void, so it falls
outside the period it undoes as often as not — which means netting cannot be
done by date. It is done by joining reversals to the entries they reverse, and
subtracting them wherever they happened.

### Posting writes, then reads, then writes

Step 5's rule holds, in the form Step 6 sharpened it into: the *first* statement
must be a write.

1. `UPDATE` — allocate `run_no`, set the status, record who posted. The
   transaction is a writer from its first statement, so a second poster waits on
   the busy timeout rather than failing an unhonourable lock upgrade.
2. Check the run exists, was a draft, and has lines. *(After the write. It reads
   inverted because it is, and it is load-bearing — see `advance.PostTx`.)*
3. Check no posted run overlaps this period.
4. Recompute every line from the ledger, inside this transaction. Refuse on any
   difference.
5. `PostBatchTx` — one entry per line, all at once.

### What posting writes, per line

| Field | Value |
|---|---|
| `customer_id` | the line's customer |
| `entry_date` | the run's `period_to` |
| `entry_type` | `interest` |
| `principal_delta_cents` | zero — `Validate` refuses anything else on an interest entry |
| `interest_delta_cents` | the charge, **positive** |
| `source_table` | `interest_run_lines` |
| `source_id` | the line's id |
| `reason` | empty — a charge is not a correction |
| `created_by` | whoever posted |

Provenance points at the line, as an advance's does, because the line is the row
that names an amount and a customer.

`entry_date` is `period_to` rather than the day somebody clicked: a run for
September charges September, whether it is posted on the 30th or on 3 October.
Otherwise a statement cut at 30 September would be missing a charge it later
turns out to have had.

### What voiding writes

One reversal per line, all in one transaction, each the exact negation of its
entry, each dated the day of the void, each carrying the void reason. The whole
run or none of it — the voucher's rule, and it matters more here, because a
partially voided run is a set of balances that is wrong in a way that looks
finished.

---

## Tasks

### 7.1 — The settings an interest rule needs

`internal/settings/defs.go`: three definitions, added to `All`.

| Key | Kind | Default | Doc |
|---|---|---|---|
| `interest.method` | choice | `none` | How interest is computed, if at all. |
| `interest.rate_bps` | int | `0` | The rate in basis points — 300 is 3%. |
| `interest.basis` | choice | `principal` | Whether interest is charged on unpaid interest too. |

`KindChoice` already exists and already refuses a value outside `Options`, which
Step 6 added for `payment.applies`. Nothing new is needed in the store.

**Done when:** a fresh database seeds all three; `PUT /api/settings` refuses
`interest.method = "monthly"` naming the three legal values, and refuses
`interest.rate_bps = "3%"`.

### 7.2 — The interest run schema

`internal/db/migrations/00006_interest.sql`, modelled on `00004_advances.sql`.

- `interest_runs` — `run_no` NULL until posted; `period_from`, `period_to` with
  `CHECK (period_to >= period_from)`; the snapshot columns `method`, `rate_bps`,
  `basis`, each in a CHECK list; `notes`; `status`; the created/posted/voided
  actor trail with `void_reason`; and the four status-implication CHECKs.
- `interest_run_lines` — `run_id`, `customer_id`, `base_cents`,
  `charge_cents CHECK (charge_cents > 0)`, `UNIQUE (run_id, customer_id)`.
- Partial unique index on `run_no`; partial unique index on
  `(period_from, period_to)` where posted; an index on `run_id`, and one on
  `customer_id` for Step 8.
- Triggers keyed off `OLD.status`: status moves one way only; a posted run is
  frozen; a posted run cannot be deleted; lines can be written, changed or
  deleted only while their run is a draft.

**Done when:** every trigger is proved by a test that tries the thing in raw
SQL, as `advance/schema_test.go` and `payment/schema_test.go` do — a
`voided → draft` transition, an `UPDATE` of a posted run's rate, a `DELETE` of a
posted run, an `INSERT` of a line against a posted run, and two posted runs
claiming the same period.

### 7.3 — The rule

`internal/domain/interest/rule.go`: `Method`, `Basis`, `Rule`, `Charge`, and
`RuleFor(Station)` reading the three settings with a validation error naming the
setting when the station is misconfigured.

`Charge` is pure and takes no context. `Rule.Valid()` refuses `none` — a run
cannot be created without a rule, and that refusal is a sentence a manager
reads, not a zero charge nobody notices.

**Done when:** a table test covers both methods, zero and non-zero rates, a
negative base, and the rounding boundaries in both directions — including the
case where half-up and round-down differ by one centavo, which is the mutation
this task is checked with.

### 7.4 — The two ledger reads

`internal/domain/ledger`: `BalancesAt(ctx, on)` and `AdvancedBetween(ctx, from,
to)`, each with a `…Tx` twin taking the caller's transaction, because posting
recomputes inside one.

Both return one row per customer with a non-zero result, so a station with 364
customers and 12 active borrowers gets 12 rows.

**Done when:** both agree with `BalanceAt` called per customer over a generated
sequence — the property test asserts the bulk read and the single read cannot
disagree — and `AdvancedBetween` excludes an advance that was voided after the
period ended.

### 7.5 — The interest domain package

`internal/domain/interest`: `Run`, `Line`, `Draft`, `Status`, `Service`,
`ValidationError`, `SourceTable = "interest_run_lines"`, the draft half (create,
recompute, delete, read, list), and `RunNosForLines` for the statement's labels.

Creating a draft computes the lines and stores them; updating one recomputes
them wholesale, as `advance.UpdateTx` replaces a voucher's lines rather than
diffing them.

**Done when:** a draft over a period with three borrowers holds three lines with
the right bases; a customer in credit produces no line; `interest.method = none`
refuses; and a period whose end is before its start refuses at the edge.

### 7.6 — Posting and voiding

`internal/domain/interest/post.go`: `PostTx`, `VoidTx`, `Entries`, `entryFor` —
the five-step order above, the overlap check, the recompute-and-refuse, and the
whole-run void.

**Done when:** posting a run over three borrowers writes exactly three entries
and moves three balances; posting twice posts once; a second run overlapping the
first is refused naming the run that already covers it; a payment posted between
preview and post makes the post refuse naming the customer; voiding returns
every balance to the centavo; and twenty concurrent posters of the same draft
produce one posted run and three entries.

### 7.7 — The API

`internal/api/interest.go` plus registration: seven routes, all `ManagerOnly`,
mirroring payments.

| Route | Access | Audit action |
|---|---|---|
| `GET /api/interest-runs` | ManagerOnly | — |
| `GET /api/interest-runs/{id}` | ManagerOnly | — |
| `POST /api/interest-runs` | ManagerOnly | `interest.draft.created` |
| `PUT /api/interest-runs/{id}` | ManagerOnly | `interest.draft.updated` |
| `DELETE /api/interest-runs/{id}` | ManagerOnly | `interest.draft.deleted` |
| `POST /api/interest-runs/{id}/post` | ManagerOnly | `interest.posted` |
| `POST /api/interest-runs/{id}/void` | ManagerOnly | `interest.voided` |

And the statement's label map gains `interest_run_lines` → *"Interest run 3"*,
beside Ref. No. and OR No.

**Done when:** `TestInterestRoutesAreRegisteredWithTheRightAccess` asserts the
table above; an encoder is refused on all seven and the refusal is audited; a
caller supplying `run_no`, `status` or a line is refused at the edge; and a
statement row produced by a run reads "Interest run 3".

### 7.8 — The screens

A rail entry, a list, and the run screen: period, the rule as the run holds it,
the preview table (code, name, base, charge), the total, and Post. Posted runs
show what they wrote and offer Void with a reason.

Everything about the screen should make the size of the act visible — the count
of farmers and the total are the two numbers a manager checks before pressing
anything.

**Done when:** walked in a browser against the real binary at 1024px, then swept
700–1440 and at 390, signed in as a manager and as an encoder; nothing scrolls
sideways, no peso figure is added up in JavaScript, and the encoder sees no rail
entry and is redirected off the route.

### 7.9 — What interest does to a payment

Not a feature: the tests that were impossible before this step. An interest run,
then a payment, under each `payment.applies` value, asserting which half
clears — plus the statement invariant holding across a run, a payment and a
void.

**Done when:** both rules are proved against real interest in the ledger rather
than against an entry written by hand, which is how Step 6 had to do it.

---

## Gotchas

**A run is the first write in the system that is unbounded.** Every other
document has a human typing every line; a run writes one entry per borrower and
364 customers is the station's real number. `PostBatchTx` takes them in one
transaction, which is right — half a run is worse than none — but it means the
batch path is load-bearing here in a way it has not been. Worth a test that
posts a run over a few hundred customers and checks it holds together.

**`BalanceAt` is a sum over entries, not a cache read.** Computing a preview
over 364 customers by calling it 364 times is 364 full scans. That is what 7.4's
bulk reads are for, and the property test that says they agree is what makes the
fast path trustworthy.

**The rate is a station-wide setting read at draft time and snapshotted.**
Changing `interest.rate_bps` does not change a draft that already exists — it is
snapshotted on the run. The screen has to say so, or somebody will change the
setting, look at an unchanged preview, and conclude the setting does nothing.

**An interest entry is the first entry type whose sign is positive on the
interest column.** `ledger.Entry.Validate` has refused anything else since Step
3, so a run that computes a negative charge does not write a wrong entry — it
fails to post at all. That is the correct failure, and there is a test for it.

---

## What this step does not do

- **No schedule.** A run happens when a manager runs it. Q1 asks whether a date
  or a button triggers it; until that is answered, automatically charging three
  hundred farmers on a timer is not a thing to guess at. The run is a document,
  so a scheduler later is a caller of the same code, not a rewrite.
- **No pro-rating, and no per-customer rate.** Both are named above as the two
  answers that would cost code.
- **No waiver or discount.** If interest needs removing for one farmer, that is
  an adjustment entry with a reason, which the ledger has had since Step 3 and
  which no screen exposes yet.
- **No report.** What a run looks like across the season belongs to Step 8, with
  the rest of the reports, and to Q3.
