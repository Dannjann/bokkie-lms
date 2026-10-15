# Step 0 — Domain Interview

**Status:** not started · **Blocks:** nothing · **Runs:** in parallel with
Steps 1–3, from day one

---

## Why this exists

Three things about how the business actually works are unknown, and two of them
shape *behaviour*, not just report layout. The architecture is deliberately
designed so this ignorance costs nothing up front — the ledger can express any
of the likely answers. **None of these questions blocks a step any more**, and
that is a deliberate choice rather than a discovery: Q1 and Q5 became settings
whose defaults do nothing, and Q3 and Q4 became assumptions built in the open.
Each question below records what was assumed and what an answer would still
cost — which is the part that matters when the answers finally come.

Guessing here is the expensive failure mode. An interest rule implemented wrong
produces numbers that look plausible and are wrong, and nobody notices until a
farmer disputes a balance.

## How to run it

**Print [questions-for-the-owner.md](questions-for-the-owner.md), not this
page.** It asks everything below in plain language, with worked examples in
pesos instead of terms like *compound* and *pro-rated*, and with room to write.
This page is the reasoning behind each question and what each answer would cost
us — useful before the visit and after it, but not something to read aloud.

1. Sit with the owner and whoever does the daily encoding —
   **the encoder often knows things the owner doesn't**, especially about
   exceptions and workarounds. Ask the encoder separately.
2. Ask for a **sample printout of each of the three legacy reports**. A real
   piece of paper answers layout questions faster than any conversation.
3. Photograph the legacy screens for "Payments From Customer" and "Compute
   Percentage" — we have screenshots of the customer and advances screens, but
   not those two.
4. Write the answers into `docs/domain-rules.md` and commit it.

---

## Questions

### Q1 — How is "Compute Percentage" calculated? ⚠️ no longer blocking

The single most important question. Sub-questions:

- Flat % applied once at release, monthly on the outstanding balance, or per
  cropping season?
- What is the rate?
- Does it compound, or is it simple interest on the original principal?
- Is it ever waived, discounted, or negotiated per customer?
- What triggers a run — a date, or someone clicking the button?
- If a farmer repays early, is interest pro-rated or charged in full?

Step 7 does not wait for the answer, for the reason Step 6 did not wait for Q5:
almost none of what this question decides is structure. The run, its preview,
its numbering and its reversal are right under every likely answer, and the
rule itself is the `interest.method`, `interest.rate_bps` and `interest.basis`
settings — defaulting to `none`, so an install nobody has configured charges
nothing at all.

Two answers would still cost code rather than a settings change, and they are
the two worth listening for on the day: **interest pro-rated when a farmer
repays early**, and **a rate negotiated per customer**. Both are recorded in
[07-interest.md](07-interest.md) under *What the answer can still cost*.

**Answer:**

*Legacy evidence ([legacy-findings.md](legacy-findings.md#q1--compute-percentage)):* per-farmer settlement, `principal × monthly rate × days ÷ 30` per advance, simple, rate typed each time (5% almost always). Built as the settlement in [07.5](07.5-settlement.md). **Confirm with the owner.**

---

### Q2 — How do farmers repay? ⚠️ shapes the schema

- Cash only, or by delivering palay/corn netted against the advance?
- If netted: who computes the delivery value, and where — this system, a
  separate scale/buying system, or on paper?
- If separate: does this system need to record the resulting peso amount only,
  or the kg and price too?

**Answer:**

*Legacy evidence ([legacy-findings.md](legacy-findings.md#q2--how-farmers-repay)):* palay, recorded here as class, sacks, kilos and price, with cost = kilos × price. Cash is squeezed in as class `CASH`. Built as palay lines on payments in [07.5](07.5-settlement.md). **Confirm with the owner.**

---

### Q3 — What is on the "Return of Investment Status" report? ⚠️ no longer blocking

Get the printout. Then confirm:

- Is it portfolio-level (capital deployed vs recovered), per-customer
  profitability, or an aging of outstanding advances?
- What period does it cover — all time, a season, a date range?
- Who reads it, and what decision do they make from it?

Step 8 does not wait for the answer. It assumes a portfolio block — capital
deployed, repaid, interest charged and collected, still outstanding — with a row
per farmer beneath it, on the reasoning that the two are the same query at two
groupings and the cheaper mistake is to build both. If the owner means only one
of them, the other is deleted rather than rebuilt.

Two answers would still cost real work: **per-cropping-season** rather than a
date range, which needs a season defined; and **cost of capital**, which is a
figure this system has never been told. Both are recorded in
[08-reports.md](08-reports.md).

**Answer:**

*Legacy evidence ([legacy-findings.md](legacy-findings.md#q3--return-of-investment-status)):* settlements in a chosen date range, each split into principal recovered and interest earned. No portfolio block. ROI's table now matches; the portfolio block is kept. **Confirm with the owner.**

---

### Q4 — What does "Recall Liquidated Transaction" do? ⚠️ no longer blocking

- Does it *view* a settled account, or *reopen* one?
- If it reopens: what happens to the balances — is that a correction workflow?

This matters more than it looks. "Reopen" implies a mutation path we must model
as reversing entries rather than edits.

Step 8 assumes **view**, and builds it so that it structurally cannot be
anything else: the report package has no write path at all. The system already
has two sanctioned ways to move a settled balance — void the document, or post
an adjustment with a reason — so if the answer is "it reopens", what gets built
is a deliberate correction workflow rather than a door left ajar in a report.

**Answer:**

*Legacy evidence ([legacy-findings.md](legacy-findings.md#q4--recall-liquidated-transaction)):* view and reprint a past settlement, **and** edit, re-save or delete (un-liquidate) it. "Liquidated" means settled on a date, not balance zero. Built as the Settled report plus settlement void ([07.5](07.5-settlement.md)). **Confirm with the owner.**

---

### Q5 — Payment application order ⚠️ no longer blocking

When a payment arrives and the customer owes both principal and interest, which
is cleared first? Is it consistent, or does it depend on the arrangement?

Step 6 does not wait for the answer: the rule is the `payment.applies` setting,
defaulting to `interest-first`. Nothing in the system charges interest until
Step 7, so no payment posted before then can be affected by the answer — when it
comes, it is a settings change and applies from that moment on. If the answer is
"it depends on the arrangement", that is a different shape — a per-customer
override — and it is worth knowing before Step 7 rather than after.

**Answer:**

*Legacy evidence ([legacy-findings.md](legacy-findings.md#q5--payment-application-order)):* interest first, in all 1,097 settlements. Matches the default.

---

### Q6 — Can one voucher really span several customers?

The Advances Entry grid has a Code and Customer Name column per row, suggesting
one Ref. No. can cover several farmers. Confirm whether that is genuinely how
they work, or just a batch-entry convenience where each row is really its own
transaction.

Either way the schema handles it — but it changes what the UI should optimise
for.

**Answer:**

*Legacy evidence ([legacy-findings.md](legacy-findings.md#q6--one-slip-several-farmers)):* yes, 261 of 745 slips, mostly spanning several days. A Ref. No. is a page that fills up, not a shared loan.

---

### Q7 — Network security (not a domain question, but ask while you're there)

Confirm the station WiFi is WPA2 with a password not shared outside the station.
The architecture serves plain HTTP over the LAN; that is a reasonable trade only
if the LAN itself is trusted.

**Answer:**

---

## Also worth collecting while on site

- [ ] Sample printouts: Current Advances, ROI Status, Recall Liquidated
- [ ] Screenshots: Payments From Customer, Compute Percentage
- [ ] A blank carbonised advance slip — needed for dot-matrix alignment in Step 9
- [ ] Make/model of the dot-matrix printer
- [ ] Which machine is always on, and whether anyone shuts it down at closing
- [ ] Whether the owner would buy a dedicated mini-PC (see Decision 5)
- [ ] How many people encode at once, and at what times of day
- [ ] What the current backup routine actually is, and when it last got restored
