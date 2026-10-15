# Step 8 — Reports

**Status:** ✅ built, gate passed · revised by [07.5-settlement.md](07.5-settlement.md) · **Depends on:** Step 7 · **Blocks:** Step 10

> **Revised, 2026-10-15.** Q3 and Q4 were answered from the legacy program
> ([legacy-findings.md](legacy-findings.md)). ROI's table is now the legacy
> one, a row per settlement split into principal returned and interest paid.
> "Liquidated" turned out to mean *settled up on a date*, not *paid off*, so
> that report became **Settled**: a list of settlements, each of which opens
> and reprints. What is written below about the original assumptions is kept
> as the record.

> **Q3 and Q4 are unanswered, and this step is built anyway** — the same
> decision taken in Step 7, and a riskier one. An interest rule guessed wrong is
> corrected by a settings change; a report guessed wrong is a page nobody uses
> and layout work thrown away. *Building on two unanswered questions* below
> records what was assumed, and what each answer would cost.

---

## Goal

**The four pages somebody prints and carries out of the room.** Everything
before this step answers a question about one farmer, at a counter, while they
are standing there. These answer questions about the whole book: who owes money,
whether the lending is working, how old the debt is, and who has finished
paying.

They are also the first screens whose output leaves the building. A statement is
read off a monitor to the farmer in front of you; a Current Advances report goes
in somebody's bag. That makes the print layout part of the work rather than a
finishing touch.

## Done when

A manager can open any of the four, choose a date or a period, and print a page
that stands on its own — station name, report name, the period it covers, who
printed it and when — with no rail, no buttons and no scrollbars on the paper.
Every figure on every report is computed on the server, the per-farmer rows add
up to the totals above them to the centavo, and a voided voucher leaves no trace
in any of the four.

---

## Where this stands

All five tasks are built and the gate has been run, printing included.

| Checked | Result |
|---|---|
| The FIFO walk against hand-worked cases | ✅ including every bucket boundary |
| 30 days vs 31, 60 vs 61, 90 vs 91 | ✅ each lands where it should |
| A payment clearing a lot exactly, and one spanning two | ✅ oldest first |
| A payment larger than everything ever lent | ✅ no negative bucket |
| An overpayment followed by a fresh advance | ✅ the credit is spent on it |
| An opening balance at cutover | ✅ ages from its own date |
| An interest charge | ✅ not aged, and shown beside the buckets |
| Five property invariants over generated ledgers | ✅ hold |
| Current Advances against `BalanceAt`, per farmer | ✅ cannot disagree |
| ROI's rows against its portfolio block | ✅ sum exactly |
| ROI's outstanding against Current Advances' total | ✅ the same figure |
| The aging buckets against the outstanding principal | ✅ sum exactly |
| Anybody reported as settled while still owing | ✅ none, over generated ledgers |
| A farmer who settles and then borrows again | ✅ not settled |
| A farmer who has never borrowed | ✅ not settled |
| A voided voucher, in all four reports | ✅ gone, once the void has happened |
| A voided voucher, in a report that stops before the void | ✅ still there, as the books read that day |
| A credit | ✅ shown, not hidden |
| A recovery rate with nothing lent | ✅ blank, not 0.0% |
| A bad date, on each of the four routes | ✅ 400, naming the parameter |
| A period that runs backwards | ✅ 400, its own sentence |
| An encoder on any of the four | ✅ refused, all four refusals audited |
| **The screens, in a browser, at counter-PC resolution** | ✅ **see below** |
| **The pages, printed at A4** | ✅ **see below** |

**Both mutations hold:**

- Counting a reversal twice turns *advanced* negative — −₱3,000.00 where
  ₱0.00 belongs — and fails in the ledger's tests and again in the report's.
  The loudest possible failure, which is what a double-count deserves.
- Moving a bucket boundary by one day fails the `exactly 30 days` case and
  nothing else, which is the point of having written the boundaries down.

**The screens were walked** in Chromium against the real binary at 1024px, swept
700–1440 and at 390, on a seeded book with money of four different ages on it.
The figures reconcile three ways: Current Advances totals ₱54,300.78, ROI says
the same is outstanding, and the aging buckets sum to ₱52,340.56 of principal
beside ₱1,960.22 of interest. An encoder sees none of the four in the rail and
is redirected off all four routes.

**The pages were printed**, which is where the faults were — five of them, all
in *Decisions and faults from building* below, and none of them visible on a
screen.

---

## Decisions and faults from building

**1. The ledger's second read grew a column, and the plan said two reads.** It
still is two, but `PrincipalLotsUpTo` became `DeltasUpTo` carrying both
principal and interest. Aging needs the principal; working out the day somebody
finished paying needs the total. One query, walked twice, rather than two
queries over the same rows.

**2. When a void counts is not what the plan assumed, and this is the important
one.** The plan said to net reversals by what they undo and never by when they
happened — which is what Step 7 does for interest, and which is wrong for a
report. `Reverse` dates a reversal the day of the void *precisely so* that "a
statement printed in February still says in March what it said in February", and
netting a March void out of February's figures breaks that from the other side.

So the report reads bound the reversal join by their own cut-off: a report over
a period reads as the books read at the end of it. That is also what keeps
*Deployed* and *Outstanding* on one page telling one story, since Outstanding
comes from `BalancesAt`, which has always worked this way.

`AdvancedBetween` keeps its unbounded join, and the difference is deliberate
rather than an oversight: it feeds an interest charge rather than a report, and
charging a farmer interest on money that has since been handed back is
indefensible whenever the handing back happened. Both functions now say so.

**3. The property test found a centavo, and it was a real bug.** An
overpayment's credit was being discarded at the moment it arose rather than
carried, so a farmer who overpaid and then borrowed again had the whole new
advance aged while their balance was net of the credit. It shrank the
counterexample to one centavo owed and two aged — exactly the size of error
nobody would ever notice on a printed page, and exactly the size that makes a
farmer's dispute unanswerable.

**4. A percentage is integer arithmetic and text.** The numerator is scaled by a
thousand before dividing, which gives tenths of a percent exactly. A recovery
rate arrived at by float division is a number nobody can reproduce by hand, and
this one is read by somebody deciding whether the lending works. Nothing lent
reports no rate at all rather than 0.0%, which would be a different claim.

**5. Five faults that only exist on paper.** The topbar printed, breadcrumb and
connection light and all. The screen's page header printed above the printed
head, so the title appeared twice and the second one explained the report to
somebody already holding it. Farmer names printed as blue underlined links.
Codes and dates right-aligned on paper while left-aligning on screen, because
the print rule caught `.numeric` — which is on those columns for tabular figures
rather than for alignment. And the table heads and the muted interest column
printed in the screen's light grey, which on paper is barely there.

The last needed `!important`, which is worth recording rather than apologising
for: the shared table styles are imported into scoped component blocks, Vue
rewrites those with a `[data-v-…]` attribute, and that outranks a plain class
selector from a global sheet.

**6. The aging report was one column too wide for the station's window.** Six
columns of figures plus a name overflowed 1024px and the interest column was cut
off the right edge — worse than omitting it, because nothing says it is missing.
The per-row principal was the column to drop: it is the four buckets beside it
added up, and the eye cannot use both. It is said in words in the totals row.

---

## Building on two unanswered questions

Q3 asks what the legacy "Return of Investment Status" report contains. Q4 asks
whether "Recall Liquidated Transaction" *views* a settled account or *reopens*
one. Neither has been asked, and unlike Q1 neither reduces to a setting.

**What was assumed, and why:**

**Q3 → a portfolio block with a per-farmer table under it.** "Return on
investment" for a lender is the relationship between capital put out and what
came back, so the totals answer *is the lending working* and the rows answer
*who*. The two are the same query at two groupings, which is what makes building
both cheaper than guessing between them: if the owner turns out to mean only one
of the two, the other is deleted rather than rebuilt.

**Q4 → a view, and structurally nothing else.** The report package has no write
path at all. "Reopen" implies a mutation, and this system already has two
sanctioned ways to move a settled balance — void the document, or post an
adjustment with a reason. If the answer is that it reopens, what gets built is a
deliberate correction workflow that writes entries, not a door left ajar in a
report.

**What an answer could still cost:**

- If ROI means **per-cropping-season profitability** rather than a date range,
  the period picker becomes a season picker and a season needs defining — which
  is Q1's territory as well.
- If ROI is expected to carry **cost of capital** — what the station's own money
  costs it — that is a figure this system has never been told and would need a
  setting and a place to enter it.
- If Recall Liquidated **reopens**, that is a new write path, a new entry type's
  worth of care, and a screen that must make the consequence unmissable.

---

## Decisions settled before building

**1. Reports get their own package, above both the ledger and the documents.**
`internal/domain/report` may ask `advance` how many vouchers a period held,
which the ledger is not allowed to know — this is the same boundary the
statement's label resolver respects, one level up. The ledger stays a thing that
knows about entries and balances.

**2. Every figure is computed on the server, in `money.Money`.** A report is the
most tempting place in the application to total a column in JavaScript, and the
one place a wrong total would be believed without checking. The browser receives
numbers as decimal strings and prints them.

**3. Reversals are netted by what they undo, never by when they happened.** A
reversal is dated the day of the void, so a voucher posted in September and
voided in October leaves a September entry and an October reversal — and any
report that sums "entries dated in the period" counts money that has been taken
back. `AdvancedBetween` already joins reversals to their targets; the new
`MovementsBetween` does the same for all four movement figures. **A voided
document must leave every report exactly as it would have been had the document
never been posted**, and that is a property test rather than a promise.

**4. Aging is derived at read time and stored nowhere.** Step 6 refused a
`payment_allocations` table because it would be a second source of truth beside
"a balance is the sum of the entries", and one a void would then have to unwind.
So aging applies what has been repaid against the oldest outstanding principal
first, computed from the entry stream when the report is opened. Nothing is
written, so nothing can drift.

The FIFO rule is a pure function over a list of dated amounts, which is what
makes it testable to the centavo without a database.

**5. Any positive principal movement is a lot; any negative one is applied.**
Aging cannot only know about advances. An `opening` entry at cutover is
outstanding principal that is already old, and an `adjustment` can move principal
in either direction. Rather than enumerate entry types, aging takes every
positive principal movement as a lot dated at its entry date, and applies every
negative one FIFO against the oldest. That is total by construction — a new
entry type in some later step cannot silently fall outside it — and it makes the
invariant clean: **the buckets sum to the outstanding principal.**

**6. Interest is not aged.** The buckets are principal only. Interest is charged
against a balance rather than lent on a day, so "how old is this interest" has no
answer the ledger can support; the outstanding interest is shown as its own
figure beside the buckets rather than folded into one of them.

**7. Liquidated means reached zero and stayed there.** A farmer is liquidated as
at a date when their balance is zero or less on that date and was positive at
some point before it — which excludes the 300 farmers who have simply never
borrowed. The day shown is the date of the entry that took them to zero.

**8. Four reports, four date questions, and the defaults are the common case.**
Current Advances and Aging are "as at" a day, defaulting to today. ROI and
Liquidated cover a period, defaulting to the current year to date. A report
opened without touching a picker should be the one somebody wanted.

**9. Manager-only, all four.** These are portfolio views. What an encoder needs
about one farmer is on the customer screen and on the statement, both of which
are already open to them.

**10. One print stylesheet, shared by all four.** `@media print` drops the rail,
the topbar and every control, and puts a head on the page: station name, report
name, the period, and who printed it when. A printed report that does not say
what it covers is a page somebody will misread six months later.

**11. No PDF, no CSV, nothing scheduled.** Decision 4 in the architecture chose
browser print over a server-side PDF library, and nothing here has changed that.
Export is added when somebody asks for a file, not before.

---

## The reports

### Current Advances — as at a day

Every farmer with a balance, code order, with principal and interest apart and
the total beside them, plus the date of their last activity. Totals at the foot.

A farmer in credit appears with a negative balance rather than being hidden: a
credit is money the station owes attention to, and dropping it would make the
total disagree with the ledger.

### Return of Investment Status — over a period

The portfolio block:

```
Capital deployed      — principal advanced in the period, net of voids
  across                 farmers, and vouchers
Repaid                — principal that came back in the period
  as a share of capital
Interest charged      — interest posted by runs in the period, net of voids
Interest collected    — interest cleared by payments in the period
Still outstanding     — the balance at the end of the period
```

Then a row per farmer: advanced, repaid, interest charged, and the balance they
ended the period on. **The rows sum to the block, exactly**, and that is the
report's one real invariant.

### Aging — as at a day

Outstanding principal in four buckets — 0–30, 31–60, 61–90, over 90 days — by
the age of the money, oldest first, with the outstanding interest shown beside
them and not in them. A row per farmer, totals at the foot.

### Liquidated — over a period

Farmers who reached zero during the period, the day they did, what they had been
advanced, and a link to the statement that shows how. Read-only, by
construction: the package cannot write.

---

## Tasks

### 8.1 — What the ledger owes the reports

`internal/domain/ledger`: two bulk reads beside the two step 7 added.

- `MovementsBetween(from, to)` → per customer: principal advanced, principal
  repaid, interest charged, interest collected. One grouped query, reversals
  joined to their targets, both ends inclusive.
- `PrincipalLotsUpTo(on)` → per customer, every positive principal movement with
  its date, net of reversals, oldest first. This is what aging is computed from.

**Done when:** both agree with `BalanceAt` over generated ledgers — advanced
minus repaid plus adjustments must equal the balance — and an advance voided
after the period ended is absent from both.

### 8.2 — The report package

`internal/domain/report`: `CurrentAdvances`, `ROI`, `Aging`, `Liquidated`, the
types they return, and `Age` — the pure FIFO function the aging report is built
on.

The package reads and never writes. It imports `ledger`, `customer` and
`advance`; nothing imports it but `api`.

**Done when:** the FIFO function is exercised against hand-worked examples
including the boundaries — an amount exactly 30 days old, a payment that exactly
clears a lot, a payment larger than every lot — and each report is checked
against a small ledger whose right answer was worked out by hand.

### 8.3 — The API

Four read routes, all `ManagerOnly`:

| Route | Query |
|---|---|
| `GET /api/reports/current-advances` | `as_of` |
| `GET /api/reports/roi` | `from`, `to` |
| `GET /api/reports/aging` | `as_of` |
| `GET /api/reports/liquidated` | `from`, `to` |

**Done when:** `TestReportRoutesAreRegisteredWithTheRightAccess` asserts the
table, an encoder is refused on all four and the refusals are audited, and a bad
date is a 400 naming the parameter while a fault is a 500 and a log line — the
distinction Step 6's statement handler was corrected for.

### 8.4 — The screens

One page per report, a shared date-picker header, and one shared print
stylesheet. Every figure arrives as a string and is printed as it arrives.

**Done when:** walked in a browser at 1024px and swept 700–1440 and 390;
**and printed** — to PDF at A4 is enough to see it — with the rail gone, the
head present, and no column cut off the right edge.

### 8.5 — The gate

The done-when at the top, the property invariants, and the two mutations: a
report that counts a reversal twice, and a bucket boundary off by a day.

---

## Gotchas

**The reports are the first read that touches the whole ledger at once.** Every
query before this one was keyed to a customer or a document. At 364 customers
and a few thousand entries this is nothing, but the shape matters: one grouped
query per report, never a loop of per-customer reads. Step 7's property test —
that a bulk read and a single read cannot disagree — is the pattern to copy.

**"As at" and "over" are different questions and must not share a picker.** A
report that silently reinterprets a date range as a cut-off is the kind of fault
that produces a plausible wrong number, which is the only kind worth fearing.

**Printing is where the money formatting will break.** `formatPesos` is built for
a screen; a printed column needs the figures to line up on the decimal point,
which is a `font-variant-numeric` and `text-align` question the screens have
already solved and the print stylesheet must not undo.

---

## What this step does not do

- **No dot-matrix.** These are A4 pages through the browser. Slips are Step 9.
- **No saved or scheduled reports.** A report is a question asked now.
- **No cost of capital, no profitability beyond interest charged.** The system
  has never been told what the station's own money costs it.
