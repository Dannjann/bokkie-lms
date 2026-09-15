# Step 4 — Customers

**Status:** 🟨 built, gate all but one item passed · **Depends on:** Step 3 · **Blocks:** Steps 5, 6, 8, 10

---

## Goal

**The first thing anybody sees.** Steps 1–3 produced a binary that installs,
logs people in, backs itself up and can prove arbitrary ledger arithmetic — and
an empty screen. This step puts the customer master behind glass: find a farmer,
add one, correct a phone number, and bring 364 existing records in from a file
rather than by typing them.

It is also where the app's visual and interaction conventions get set, because
it is the first screen that is not a login form.

## Done when

An encoder can find any of the imported customers by code, name or phone and get
an answer immediately; can add a new one that is assigned the next `BA-` number
with no chance of a collision; and a manager can deactivate one — with every
change in `audit_log`, and the import proved all-or-nothing by a file whose last
row is bad.

---

## Where this stands

All seven tasks are built. Everything the gate asks for has been checked except
the last line of 4.7, which needs a browser and a person.

| Checked | Result |
|---|---|
| Codes unique, monotonic, never reused over generated sequences | ✅ holds |
| 50 concurrent creates under `-race` | ✅ 50 distinct codes |
| Allocation past `BA-9999` | ✅ refused, nothing written |
| A lower-case `ba-0400` in an import file | ✅ allocator continues at `BA-0401` |
| A bad row at line 300 | ✅ nothing written, the line is named |
| Dry run, both front doors | ✅ table and audit log both untouched |
| Searching `%` and `_` | ✅ literal, not a wildcard |
| `0917`, `0917-123`, `09171234567` | ✅ all find a number stored as `0917-123-4567` |
| An encoder deactivating a customer | ✅ refused, and the refusal is audited |
| A caller supplying a code, on create or update | ✅ refused at the edge |
| Import, end to end from the command line | ✅ 3 customers, one `console` audit row |
| Re-running the same import | ✅ all three rows rejected as already on the books |
| SPA shell and lazy chunks for every new route | ✅ served |
| **The screens, in a browser, at counter-PC resolution** | ⬜ **outstanding** |

**Both mutation checks hold.** A test that cannot fail reports success over
exactly the ground it was meant to be watching, so each of the two tests
carrying this step's real weight was checked against a deliberate break:

- Replacing the allocator with a read-then-write made
  `TestConcurrentCreatesGetDistinctCodes` fail immediately.
- Making deactivation delete the row made `TestPropertyCodesAreNeverReused` fail
  on its first generated sequence.

## Decisions taken while building

**1. The read-then-write allocator fails louder than expected, and that is
worth knowing.** The plan justified allocating inside the `INSERT` on the
grounds that a deferred transaction lets two encoders read the same `MAX`. The
mutation test showed something worse: of 50 concurrent creates, 43 failed with
`SQLITE_BUSY` and only 7 got through. Reading inside a deferred transaction and
then writing means asking for a write lock the transaction did not reserve, so
the realistic symptom is not a duplicate code at all — it is the counter seizing
up under two people working at once, which is much harder to attribute to its
cause. The single-statement allocator avoids both.

**2. `Draft` having no code field closes the API edge for free.** The plan gave
the reason as making the rule a property of the type rather than something to
remember. It does more than that: `decodeJSON` already rejects unknown fields,
so a request carrying `"code"` is refused with a 400 before any handler runs, on
create and on update alike. Nothing had to be written to make that true.

**3. The import's audit entry is written by the domain package, not the
handler.** The plan put the entry at the self-audited route. Writing it inside
`Apply`'s own transaction instead means the row and the customers it describes
commit together, and — more usefully — that the command line and the browser
produce the same audit row rather than two that have to be kept in step. The
route is still registered `mutateSelfAudited`, and the reason recorded on it is
still true: the wrapper cannot tell a preview from a real import.

**4. One existing test had to change.**
`TestUnknownAPIPathReturnsJSONNotHTML` used `/api/customers` as its example of
an unknown path, which was accurate when it was written. It now asks for a path
that is not a route.

---

## Decisions settled before building

Five questions were answered up front, because each of them changes what gets
built rather than how.

**1. The import carries master data only.** Code, name, address, cell number,
active flag, notes — and no money anywhere. `00-architecture.md` puts opening
balances in Step 10 as `opening` ledger entries, and pulling them forward into
Step 4 would mean an import bug writes wrong numbers into the one table that
cannot be edited afterwards. This step cannot post a ledger entry at all, which
is the cheapest possible guarantee that it will not post a wrong one.

**2. Codes are assigned by the server and are never editable.** The code is the
number written on the farmer's paper slip. Changing it would orphan every slip
already handed out, so the domain type simply has nowhere to put a
caller-supplied code. The import is the one exception and preserves whatever the
CSV carries, so the legacy numbering survives the cutover intact.

**3. Customers are never deleted — only deactivated.** This falls out of
decision 2 rather than from principle. Codes are allocated as a high-water mark,
so deleting `BA-0365` would hand that same code to the next customer created,
and two different farmers would hold the same code on paper slips months apart.
Deactivation is reversible, audited, and keeps the answer to "who was BA-0365?"
available forever.

**4. Search is server-side and paginated.** At 364 rows a client-side cache
would be faster, but this query is the one Step 5's voucher-line picker and Step
8's reports will both reuse, and it is worth having one query path rather than a
cache-invalidation story per screen. On a LAN the round trip is not perceptible.

**5. The import has two front doors over one engine.** A CLI command for the
person standing at the host at cutover, and a manager-only upload for the owner
who wants to re-run it later without a terminal. Both call the same parser and
the same apply step, so there is one set of rules and one place they can be
wrong.

---

## Customer semantics

### Code allocation

Codes are `BA-` followed by exactly four digits, zero-padded, which is what
makes lexicographic order and numeric order the same thing and lets `MAX()` be
the allocator.

Allocation happens **inside the `INSERT`**, never as a read followed by a write:

```sql
INSERT INTO customers (code, name, address, cell_no, active, notes, created_at, updated_at)
SELECT printf('BA-%04d', COALESCE(MAX(CAST(substr(code, 4) AS INTEGER)), 0) + 1),
       ?, ?, ?, ?, ?, ?, ?
  FROM customers
 WHERE code GLOB 'BA-[0-9][0-9][0-9][0-9]';
```

The database opens its transactions deferred — there is no `_txlock=immediate`
in `internal/db` — so two encoders clicking Save at the same moment would both
read `364` and both try to write `BA-0365`. One statement removes the window
entirely, and the `UNIQUE` index on `code` is the backstop if it is ever
reintroduced.

Because nothing is ever deleted, `MAX` is a true high-water mark and a code is
never reused.

`BA-9999` is the end of the sequence. Allocation past it **fails loudly** rather
than producing `BA-10000`, which would sort before `BA-9999` and quietly break
the allocator, every listing and every report at once. Widening the format is a
deliberate migration, not something to discover at the counter. At 364 customers
this is decades away; the check costs one comparison.

`code` is `COLLATE NOCASE UNIQUE` but `GLOB` is case-sensitive, so a lower-case
`ba-0365` in an import file would slip past the high-water scan and then collide
on the unique index. Codes are upper-cased on the way in.

### Lifecycle

Create, edit, deactivate, reactivate. No delete, and no merge — two farmers may
genuinely share a name, and a wrongly-created duplicate is deactivated like
anything else.

`code`, `created_at` and `id` are fixed at creation. Everything else is
editable, and every edit writes an `audit_log` row with the before and after
state, which the route registry gives us for free.

### Search

```
GET /api/customers?q=&status=active|inactive|all&page=&per_page=
```

A substring match on `code`, `name` and `cell_no`, ordered by `code`, defaulting
to active customers only.

`LIKE` metacharacters in `q` are escaped with an explicit `ESCAPE` clause. A
farmer's name containing an underscore is unlikely; somebody typing `%` into a
search box to see what happens is not, and it would silently return everyone.

Phone numbers are **normalised on save** to digits with an optional leading `+`,
so a number typed `0917-123-4567` at one counter and `0917 123 4567` at another
are the same string in the table, and searching `0917` finds both. Validation is
otherwise deliberately loose: over-strict phone rules are a nuisance at a
counter and catch nothing that matters.

### The import contract

Header-driven and order-independent, so a column added to the export later does
not shift everything by one:

```csv
code,name,address,cell_no,active,notes
BA-0001,Juan dela Cruz,Pintor Gamu,0917-123-4567,true,
```

- `code` and `name` are required; the rest are optional.
- The UTF-8 BOM is stripped. Excel on Windows writes one, and that is where this
  file will come from.
- `active` accepts `true/false`, `1/0`, `yes/no`; absent means active.
- `code` must match `BA-` plus four digits. A legacy code shaped otherwise is
  rejected rather than accommodated — it would be invisible to the high-water
  scan and would break the allocator the first time somebody added a customer.
  Finding out at the dry run, at the station, is what the dry run is for.
- Unknown columns are an error, not a shrug. A `cellno` header that should have
  been `cell_no` would otherwise import 364 customers with no phone numbers and
  look like it worked.

**All-or-nothing.** A bad row on line 300 imports nothing, and the report names
every rejection with its line number. A half-imported master is not a smaller
problem than a failed one — it is the same problem plus the belief that the job
is done.

**Duplicates are rejected, never merged**, whether the collision is inside the
file or against a code already in the database. An upsert that silently
overwrites a name somebody corrected by hand is a data-loss path that leaves no
trace, and re-running an import is exactly when it would happen.

One `audit_log` entry for the whole import, not 364 — 364 rows would bury every
other entry from that day. The entry records the file's name, its **SHA-256**
and the range of codes written, so the question "which file produced this
master?" has an answer later, in the same spirit as signed releases and verified
backups.

---

## Tasks

### 4.1 — The customer domain package

`internal/domain/customer`, owning the table the way `internal/auth` owns users
and `internal/domain/ledger` owns entries.

```go
type Customer struct {
    ID                   int64
    Code                 string    // BA-0001, assigned here, never by a caller
    Name, Address        string
    CellNo, Notes        string
    Active               bool
    CreatedAt, UpdatedAt time.Time
}

// Draft is everything a caller is allowed to decide. It has no Code field on
// purpose: "assigned by the server, never editable" is then a property of the
// type rather than a rule every handler author has to remember — the same trick
// auth.User uses by having no password-hash field.
type Draft struct{ Name, Address, CellNo, Notes string }
```

Validation and normalisation live here, not in the handlers: name required and
trimmed, address and notes bounded, cell number normalised as above. Reject with
messages meant for whoever is standing at the counter — `statusError` already
carries that distinction through to the response.

Follow `auth`'s `queryExecer` interface so one implementation serves both
`*sql.DB` and `*sql.Tx`; the API's `TxHandler` hands handlers a transaction, and
the import needs the same code outside a request.

**No migration in this step.** `customers` already exists — Step 3 created it so
`ledger_entries` could have a real foreign key from its first row. Adding a
`CHECK` on the code format now would mean rebuilding a table that
`ledger_entries` points into, which is the rebuild that decision deliberately
avoided. The format is enforced in Go, where this package is the only writer. No
new indexes either: the table is tiny, and an index cannot serve a `LIKE '%x%'`
predicate anyway.

**Done when:** the package creates, reads, updates and deactivates against an
in-memory database with no HTTP layer involved, and a malformed draft is refused
with a message a counter clerk could act on.

---

### 4.2 — Code allocation, proved under concurrency

The single-statement allocator above, plus the `BA-9999` refusal.

This is the task that gets a **property test**, in the house style: over
arbitrary interleavings of create, deactivate, reactivate and import, assert
that codes are unique, that the sequence is monotonic, and that no code is ever
reused after a deactivation. Reuse is the failure this whole design exists to
prevent, and it is the one that would surface as two farmers disputing the same
slip rather than as an error.

Separately, N concurrent creates under `-race` must yield N distinct codes. Check
the test can fail: swap the allocator for a read-then-write and confirm it does.
A concurrency test that has never been seen to fail is reporting on nothing.

**Done when:** the property holds over thousands of generated sequences, 50
concurrent creates under `-race` produce 50 distinct codes, and the read-then-write
mutation is caught.

---

### 4.3 — Search and listing

`List(ctx, Query) (Page, error)` — substring match, status filter, ordered by
code, with a total count for the pager.

**Done when:** a search for `%` returns matches for a literal percent sign
rather than every customer, and `0917` finds a number stored from
`0917-123-4567`.

---

### 4.4 — The CSV import engine

Two functions, so that deciding and doing are separable and the dry run is
genuinely the same code as the real thing:

```go
// Plan parses and validates. It touches no database and returns what would
// happen, including every rejection with its line number.
func Plan(r io.Reader, existing CodeSet) (Report, error)

// Apply writes a plan in one transaction, or writes nothing.
func (s *Service) Apply(ctx context.Context, rep Report, by Actor) error
```

`Actor` is declared in this package rather than borrowed from
`internal/domain/ledger`. It is the same two fields, and the duplication is the
point: reusing the ledger's type would make the customer package import the
ledger to record who typed a name, which is precisely the dependency 4.6 goes to
some trouble to avoid.

A dry run is `Plan` alone. That is what makes the preview trustworthy: it cannot
diverge from the real import, because it *is* the real import minus the commit.

**Done when:** a file whose 300th row is bad imports nothing and reports that
row; a BOM, CRLF line endings, a duplicate inside the file, a duplicate against
the database, an unknown header, and a bad `active` value are each rejected with
a message naming the line.

---

### 4.5 — Two front doors

**CLI.** `bokkie import-customers -check file.csv` prints the report and writes
nothing; without `-check` it applies. This mirrors `bokkie update -check DIR`,
which is the shape of every risky operation in this system already. It audits as
the `console` actor — `audit.ConsoleUser` exists for exactly this. The service
does not need to be stopped: WAL handles a second writer.

**Web.** `POST /api/customers/import`, multipart, manager-only, with a `dry_run`
flag, wrapped in `http.MaxBytesReader`. This is the first non-JSON endpoint in
the system; keep it the only one.

Registered with `mutateSelfAudited`, because the audit entry for a preview and
for a real import are different events and the wrapper cannot tell which
happened. That is a genuine use of the exception mechanism rather than a way
around it, and the reason string is printed by the coverage test.

**Done when:** the same file produces the same report through both doors, and a
dry run through either leaves the table untouched.

---

### 4.6 — The API surface

| Route | Access | Why |
|---|---|---|
| `GET /api/customers` | Authenticated | encoders look people up all day |
| `GET /api/customers/{id}` | Authenticated | |
| `POST /api/customers` | Authenticated | adding a farmer is counter work |
| `PUT /api/customers/{id}` | Authenticated | fixing a phone number is counter work |
| `POST /api/customers/{id}/active` | **ManagerOnly** | the nearest thing to deletion |
| `POST /api/customers/import` | **ManagerOnly** | |

The detail response carries the customer's balance, **composed in the API layer**
from `customer.ByID` and `ledger.BalanceFor`. Neither domain package imports the
other, which keeps Step 3's boundary intact: the ledger still knows nothing but
entries and balances, and the customer package knows nothing about money.

The list does **not** carry balances. Every one is zero until Step 5 exists, and
joining `customer_balances` inside the customer package would put a ledger
concept there to display a column of zeroes.

**Done when:** every route's access level and audit style is asserted by a test,
and an encoder attempting to deactivate a customer is refused and the refusal
appears in `audit_log`.

---

### 4.7 — The screens

- `/customers` — search box, status filter, PrimeVue `DataTable` in lazy mode
  against 4.3's pagination. Columns: code, name, cell, address, status.
- `/customers/new` — the create form; the code shows as "assigned on save".
- `/customers/:id` — the edit form, read-only code, balance panel, and the
  Active toggle for managers.
- A Customers link in `AppHeader`.

Build only what these screens need. A reusable list/detail abstraction cannot be
designed from one example, and the second example — Step 5's inline-editing
voucher grid — is the one the architecture already flags as unusual. Generalise
then, with two real cases in hand.

**Done when:** a customer can be found, created, edited and deactivated from a
browser, and the screens work in an `--app` window at counter-PC resolution.

---

## Verification

```bash
go test ./internal/domain/customer/... -race
go test ./... -run Property -count=10
make ci
```

- Codes stay unique and are never reused, over thousands of generated sequences.
- 50 concurrent creates produce 50 distinct codes under `-race`.
- A bad row at line 300 imports nothing.
- A dry run through either front door leaves the table untouched.
- Searching `%` does not return everyone.
- An encoder cannot deactivate a customer, and the attempt is audited.

Then, by hand, with a real export: dry-run the 364 legacy customers, read the
report, import them, and confirm the next customer created is assigned the code
after the highest imported one.

## Risks

| Risk | Mitigation |
|---|---|
| Two encoders get the same code | Allocation inside the `INSERT`; `UNIQUE` index behind it; concurrency test |
| A code is reused after a deactivation | Nothing is deleted, so `MAX` is a high-water mark; property test |
| A silent half-import | `Plan`/`Apply` split, one transaction, all-or-nothing |
| An import quietly overwrites corrected data | Duplicates rejected, never merged |
| A mistyped CSV header imports empty columns | Unknown and missing headers are errors |
| The legacy export has codes outside `BA-####` | Rejected at dry run, at the station, before anything is written |
| `BA-9999` is reached | Allocation refuses rather than breaking sort order |

---

## What this step deliberately does not do

No balances on the list, no customer statement (Step 6), no duplicate merging,
no `-update` import mode, and no reusable grid abstraction.

No ledger entries of any kind. This step cannot post one, which is what keeps
the import — the only bulk write in the system — structurally unable to put a
wrong number into the record. Opening balances remain Step 10's, as `opening`
entries dated at the cutover.
