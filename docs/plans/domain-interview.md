# Step 0 — Domain Interview

**Status:** not started · **Blocks:** Steps 6, 7, 8 · **Runs:** in parallel with
Steps 1–3, from day one

---

## Why this exists

Three things about how the business actually works are unknown, and two of them
shape *behaviour*, not just report layout. The architecture is deliberately
designed so this ignorance costs nothing up front — the ledger can express any
of the likely answers — but Steps 7 (interest) and 8 (reports) **cannot be
started** until these are answered.

Guessing here is the expensive failure mode. An interest rule implemented wrong
produces numbers that look plausible and are wrong, and nobody notices until a
farmer disputes a balance.

## How to run it

1. Print this page. Sit with the owner and whoever does the daily encoding —
   **the encoder often knows things the owner doesn't**, especially about
   exceptions and workarounds.
2. Ask for a **sample printout of each of the three legacy reports**. A real
   piece of paper answers layout questions faster than any conversation.
3. Photograph the legacy screens for "Payments From Customer" and "Compute
   Percentage" — we have screenshots of the customer and advances screens, but
   not those two.
4. Write the answers into `docs/domain-rules.md` and commit it.

---

## Questions

### Q1 — How is "Compute Percentage" calculated? ⚠️ blocks Step 7

The single most important question. Sub-questions:

- Flat % applied once at release, monthly on the outstanding balance, or per
  cropping season?
- What is the rate?
- Does it compound, or is it simple interest on the original principal?
- Is it ever waived, discounted, or negotiated per customer?
- What triggers a run — a date, or someone clicking the button?
- If a farmer repays early, is interest pro-rated or charged in full?

**Answer:**

---

### Q2 — How do farmers repay? ⚠️ shapes the schema

- Cash only, or by delivering palay/corn netted against the advance?
- If netted: who computes the delivery value, and where — this system, a
  separate scale/buying system, or on paper?
- If separate: does this system need to record the resulting peso amount only,
  or the kg and price too?

**Answer:**

---

### Q3 — What is on the "Return of Investment Status" report? ⚠️ blocks Step 8

Get the printout. Then confirm:

- Is it portfolio-level (capital deployed vs recovered), per-customer
  profitability, or an aging of outstanding advances?
- What period does it cover — all time, a season, a date range?
- Who reads it, and what decision do they make from it?

**Answer:**

---

### Q4 — What does "Recall Liquidated Transaction" do? ⚠️ blocks Step 8

- Does it *view* a settled account, or *reopen* one?
- If it reopens: what happens to the balances — is that a correction workflow?

This matters more than it looks. "Reopen" implies a mutation path we must model
as reversing entries rather than edits.

**Answer:**

---

### Q5 — Payment application order ⚠️ affects Step 6

When a payment arrives and the customer owes both principal and interest, which
is cleared first? Is it consistent, or does it depend on the arrangement?

**Answer:**

---

### Q6 — Can one voucher really span several customers?

The Advances Entry grid has a Code and Customer Name column per row, suggesting
one Ref. No. can cover several farmers. Confirm whether that is genuinely how
they work, or just a batch-entry convenience where each row is really its own
transaction.

Either way the schema handles it — but it changes what the UI should optimise
for.

**Answer:**

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
