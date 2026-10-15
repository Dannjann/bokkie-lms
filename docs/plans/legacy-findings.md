# What the Legacy Program Says

**Source:** the TLSADV Visual FoxPro program (`TLSMAIN.PRG`, `FORMS/*.SCT`,
`REPORTS/*.FRX`) and its live tables (`DATA/*.DBF`), read on 2026-10-15.
**Status:** evidence, not answers. Each finding below is what the old system
*does*. The owner still has to confirm it is what the business *means*. The
code shows what the encoder was made to do, and the data shows what they
actually did. Neither shows intent.

The data contains real farmer names and balances. Nothing below names a person.
Every figure is a count or a rate, and it should stay that way.

> **Implemented, 2026-10-15,** in [07.5-settlement.md](07.5-settlement.md). The
> "Against what Bokkie built" paragraphs below describe Bokkie *before* that
> step; they are kept because they are why it was done. Two findings were
> deliberately not acted on: the overpayment question (still credit, pending
> the owner) and the O.R. pad (sequence kept, pending the owner).

---

## The one thing that changes everything: "liquidation"

The old system has no interest *runs*. Nothing is ever charged across all
farmers at once. The central act is a **liquidation**: one farmer, on one
day, settled up. "Compute Percentage" (`PERTRN`) is that screen:

1. Pick a farmer. The screen loads every advance and every palay delivery
   not yet liquidated (`PDATE` empty).
2. Type a **cut-off date** and a **rate**. Advances and deliveries dated after
   the cut-off are dropped from this settlement.
3. The screen computes interest on each advance separately, from that
   advance's own date to the cut-off. It also computes interest on any
   principal carried over from the last liquidation, from the last liquidation
   date to the cut-off.
4. Deliveries are applied **interest first, then principal**.
5. **LIQUIDATE** stamps every included advance and delivery with the cut-off
   date. It writes the result to the farmer's record (carried principal, carried
   interest) and appends one row to `FARROI`, the settlement history.

All 1,097 settlements in `FARROI` have the remark `PAYMENT`. In the old system,
a liquidation happens because the farmer delivered.

## Q1 — Compute Percentage

```
interest on one advance = principal × rate × days × 12 / 360      (rounded to centavos)
days                    = cut-off date − advance date             (calendar days)
```

`12/360` makes the typed rate a **monthly rate on a 30-day month**: 5% for 30
days is 5%, and 5% for 45 days is 7.5%. Every one of the 7,072 liquidated
advance rows matches this formula to the centavo.

| Sub-question | What the program does |
|---|---|
| Flat, monthly, or per season? | Simple daily accrual per advance, at a monthly rate, up to a cut-off date |
| What rate? | Typed at each liquidation. Of 1,097 settlements: 5% in 1,063, 3% in 29, 2% in 2, 8% in 1, 0% in 2 |
| Compound? | **No.** Carried interest (`PRVINT`) is added to the total but never charged interest itself. Interest on carried-over amounts is charged on principal only, in all 231 cases where that happened |
| Waived or negotiated? | Yes, in effect. The rate is typed per settlement. 6 farmers have been settled at more than one rate, and 2 settlements were at 0% |
| What triggers it? | A person, one farmer at a time, when that farmer delivers palay |
| Pro-rated on early repayment? | Yes, inherently. Interest is for the days that actually passed, and nothing else |

**Against what Bokkie built:** neither `monthly-on-balance` nor
`flat-on-advance` produces these numbers. Both "would still cost code" answers
named in [07-interest.md](07-interest.md) turned out true: interest is
pro-rated by the day, and the rate is chosen per settlement. A station-wide
`interest.rate_bps` can hold the default (500), but the settlement needs to
carry its own rate.

## Q2 — How farmers repay

**Palay, recorded line by line in this system.** `PAYTRAN` has `CLASS`,
`SACKS`, `KILOS`, `UPRICE` and `COST`. "Payments From Customer" (`PAYTRN`)
computes `cost = kilos × price` itself, on the grid. Classes are grade or
variety codes (`SLG`, `YCS`, `DOLG`, `YCDG`, `SC10`, with suffixes like
`CB`, `BASA`, `MIXED`). Prices range from about ₱10 to ₱16 a kilo.

Cash comes in through a workaround. Ten rows have the class `CASH`, entered as
kilos = pesos at price 1, or as a cost with zero kilos. That is the encoder
fitting cash into a palay screen.

**Against what Bokkie built:** `payments` holds a peso amount and a `method`,
and the migration says outright that if the delivery value is computed here,
what is needed is "a delivery document that produces a peso figure and feeds
a payment". That is now the answer: the station keeps class, sacks, kilos and
price, and the encoder will expect to type them.

**A new question this raises.** In **536 of 1,097** settlements the palay was
worth more than the debt, which leaves the farmer's principal negative. 133
farmers have a negative carried principal on file today. Yet **no** later
settlement ever *started* from a negative principal. So the surplus is not
carried forward as credit in practice. It is presumably paid out in cash and
the stale figure cleared some other way. Bokkie keeps an overpayment as credit
against the next advance. *Ask: when the palay is worth more than he owes, does
he get the difference in cash on the day?*

## Q3 — Return of Investment Status

`REPROI` is a **list of settlements between two dates the user picks**. It
filters `FARROI` on the day the liquidation was saved, not on the cut-off date.
Each row has Code, Date, Customer Name, **Principal** (the part of the delivery
that went to principal), **Interest** (the part that went to interest) and
**Amount** (the whole delivery). Totals are labelled *Return / Paid / Total*.

It answers "how much came back in this period, and how much of it was
profit?" It is collections split into principal and interest, farmer by farmer.
It is not a portfolio view: there is no capital deployed, no outstanding and
no cost of capital. There is no cropping season either. It is a free date
range.

**Against what Bokkie built:** the per-farmer rows and the date range fit. The
portfolio block (deployed, outstanding, repaid share) is something the old
report never had. Per [08-reports.md](08-reports.md), that half is the one to
delete if the owner doesn't miss it. One trap: when the delivery exceeded the
balance, the old row shows Principal and Interest capped at what was owed,
but Amount shows the whole delivery. So in the old report, Principal +
Interest ≠ Amount on those rows.

## Q4 — Recall Liquidated Transaction

**Both.** `RECATRN` loads one farmer's settlement by farmer and liquidation
date. It shows it exactly as computed and reprints the settlement slip
(`PERTRNP`). That much is a view. But the screen also has **Edit** (rate,
cut-off and payment become editable), **Save** (runs the save again, which
appends a *second* `FARROI` row), and **Delete**. Delete un-liquidates: it
clears the liquidation stamp so the advances and deliveries become open again,
and it deletes the `FARROI` row.

As written, Delete's `REPLACE ALL PDATE …` has no farmer filter, so it would
clear the stamp on *every* farmer's records, not just this one. Whether the
encoder has ever pressed it is worth asking, gently.

**Against what Bokkie built:** Bokkie's "Liquidated" report lists farmers
whose balance *reached zero*. In the old system "liquidated" means **settled
up on a date**, whether or not anything is still owed. 561 settlements carried
principal forward. The view half of Q4 needs a settlement record to exist
before it can be recalled. The reopen half is the correction workflow that
[domain-interview.md](domain-interview.md) anticipated: void the settlement and
redo it. It is not an edit.

## Q5 — Payment application order

**Interest first, always.** Both the on-screen computation and the stored
history agree. All 1,097 settlements reconcile exactly to interest-first.
Bokkie's default `interest-first` is right. No setting change is needed.

## Q6 — One slip, several farmers

**Yes, and often.** 261 of 745 Ref. Nos. cover more than one farmer, up to 63
on one slip. Of those 261, **243 span more than one date.** So a Ref. No. is not
one visit and not a shared loan. It behaves like a **page in a book** that
fills up over days. Each row is still one farmer's own advance, liquidated
separately. That suggests the voucher screen should make adding the next row
for a different farmer cheap. It does not need to model a shared loan.

## Q7 — Who may do what

The old system has no permissions. `USERCODE` has two users, both level 1.
The login reads `LEVEL` into `nLevel` and nothing ever checks it. Anyone who
can log in can do everything, including Recall's Delete. That doesn't say what
the owner *wants*, so ask anyway. It does mean any restriction Bokkie
introduces will be new to them.

## Q9 — Carrying on from the old system

- **Last Ref. No.: 1425.** `TRNADV` (the counter) and the highest `INVNO` in
  `ADVTRAN` agree. `refno.start` should be **1426**. Re-check on the day,
  because it will move if they keep encoding.
- **There is no O.R. No. in the old system.** Deliveries have no reference
  number of any kind. The `OR#` in `DONMEMO.MEM` is a printer's authority
  line with an issue date and a validity date, not a receipt counter.
  `orno.start` has nothing to continue from. Ask whether a paper O.R. pad
  exists outside the system before setting it.

## Observations that weren't questions

- **Usage dropped sharply after 2010.** Most advance slips date from 2007–2010
  (723 of 745), with a handful since. 19,497 advance rows and 2,472 delivery
  rows are soft-deleted, mostly from 2007–2010. The tables were modified today,
  so it is still in use, but at a fraction of the old volume. *Ask whether the
  station still lends at the old scale, or whether most of it now happens on
  paper.*
- **Advances are goods as often as cash.** The commonest particulars are
  `CASH`, then fertiliser (`UREA`, `16-20-0`), diesel by the litre, and
  agrochemicals by brand. The particular is free text. Nothing tracks stock.
- **Customer codes are 7 characters** for 343 of 348 farmers. There are 344
  active and 1 inactive.
- `CLRTRAN.PRG` and `RECATRAN.PRG` are maintenance scripts that un-liquidate
  every transaction and empty `FARROI`. They are not on any menu, but they
  exist. They may be why `FARROI` only starts in October 2007, even though
  the advances go back further.
