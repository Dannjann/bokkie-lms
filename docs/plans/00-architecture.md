# Bokkie LMS — Architecture

**Status:** approved · **Scope:** Decisions 1–5, Steps 1–3

---

## Context

TLS Palay & Corn Buying Station (Pintor Gamu, Isabela) runs a Visual FoxPro
application on DBF files (`d:\tlsadv\data\trnadv.dbf`) to manage cash advances
to farmers. It has ~364 customers and advance vouchers numbered into the 1400s.

The legacy system does five things: maintain a customer master (`BA-0001`, name,
address, cell, active flag, carried-forward previous balance), enter multi-line
advance vouchers against a Ref. No., record payments from customers, compute an
interest/percentage figure, and print three reports (Current Advances, Return of
Investment Status, Recall Liquidated Transaction). Its two "System Utilities" —
*Back-up Files* and *Re-Index Files* — are tells: the data layer is fragile,
indexes rot, and recovery is manual.

The replacement must keep **all data local with nothing sent to the cloud** and
be reachable from several PCs plus phones/tablets on the station's own network.
The client machines are mostly Windows, but **the host machine must be free to
be Windows, macOS, or Linux** — the station should not be locked to one box or
one OS licence. Existing DBF data will **not** be migrated; the system starts
fresh.

**The outcome we want:** a system a non-technical operator can start by
double-clicking one file, that several staff can use at once without corrupting
anything, that backs itself up, and whose money figures are reproducible and
auditable years later.

### Requirements, as confirmed

| Question | Answer |
|---|---|
| Concurrency | Several PCs **plus phones/tablets** |
| Existing DBF data | **Start fresh** — no migration |
| Stack constraint | Open to the best tool |
| Printing | **Both** dot-matrix slips and A4 reports |
| Host OS | Windows, macOS, **or** Linux — decided at deploy time |

---

## Decision 1 — LAN-hosted web app, not a standalone desktop app

**Serve the app from one host machine on the station's network; every other
machine and phone reaches it through a browser.**

The requirement for several PCs *plus phones/tablets* decides this on its own. A
standalone desktop app cannot serve a phone, and the usual workaround — putting
a SQLite/DBF file on a Windows share and pointing multiple desktop clients at it
— is how the legacy system's "Re-Index Files" button came to exist. SQLite over
SMB is explicitly unsupported and corrupts under concurrent writes. For
financial records that is not an acceptable risk.

**Staff never have to see a browser.** On each counter PC the shortcut is:

```
msedge.exe --app=http://bokkie.local:8080
```

That opens a chromeless application window — no address bar, no tabs. It looks
and behaves like a desktop app while being a single shared server underneath.
Phones and tablets just open the URL normally.

## Decision 2 — Go backend + Vue 3 SPA, shipped as one binary

**Backend: Go. Frontend: Vue 3 + TypeScript + PrimeVue, compiled and embedded
into the Go binary.** The entire system ships as **one executable** — no PHP, no
Node, no XAMPP, no runtime to install or break.

Why Go carries this:

- **`go:embed` + `CGO_ENABLED=0` gives a genuinely single static binary.** Cross
  compile it from a Mac with one command. Copy one file to the host and run it.
  Nothing else to install, nothing to go wrong in six months.
- **`modernc.org/sqlite` is a pure-Go SQLite** — no cgo, so darwin → windows
  cross-compilation is trivial. (`mattn/go-sqlite3` would force a Windows
  cross-toolchain; avoid it. See the warning below.)
- **Raw dot-matrix printing needs OS-level printer access** — the Windows
  spooler API via `golang.org/x/sys/windows`, or CUPS on macOS/Linux. This is
  the thing a browser fundamentally cannot do (Decision 4).
- **Host-OS freedom comes from build flags, not a rewrite.** Same source, five
  targets (Decision 5). Only two small packages are OS-specific, both behind
  build tags.
- Handles 5–10 concurrent users on SQLite WAL without breaking a sweat.

Why Vue rather than server-rendered Go templates: this app is wall-to-wall data
grids — the Advances Entry line-item grid especially — and PrimeVue's DataTable
does inline-editing grids properly. Plain Vite SPA, **not** Nuxt: there is no SSR
requirement, and a Vite `dist/` folder embeds into the binary cleanly.

> ⚠️ **The pure-Go SQLite driver is load-bearing, not a preference.** Swapping to
> a cgo driver breaks cross-compilation for every non-host platform at once and
> silently ends the host-OS flexibility in Decision 5. This warning belongs in a
> code comment next to the import.

## Decision 3 — On Flutter/Dart

Flutter is a good framework, and this is not a criticism of it. For *this*
system it is the wrong first layer, for three concrete reasons:

1. **It only solves the client half.** Multiple PCs and phones sharing data
   means a server process regardless. Choosing Flutter doesn't remove that work
   — it adds a second codebase alongside it.
2. **The tempting shortcut is dangerous.** The way Flutter desktop apps usually
   go multi-user is a shared SQLite file on a network drive. That corrupts under
   concurrent writes — rebuilding the legacy system's central flaw.
3. **Printing is weaker** in Flutter desktop than in a browser + server-side
   spooler combination, and we need both paths.

**This is a deferral, not a rejection.** The Go backend exposes a plain JSON API,
so a native Flutter app for field collectors on Android later plugs into the
same API with no backend changes. That is a better time to spend the Flutter
effort — when offline-first mobile actually earns it.

## Decision 4 — Two separate printing paths

**A4 reports → browser print.** Render the report as HTML with a `@media print`
stylesheet and let the browser print it. Free, looks far better than the legacy
output, works identically from any machine, needs no server-side PDF library.
Add PDF export later only if reports must be archived as files.

**Dot-matrix slips → server-side raw ESC/P.** A browser *cannot* send raw control
codes to a printer; anything it prints goes through the graphics driver, which is
slow and ugly on continuous form. So the dot-matrix hangs off the host machine
and the server writes ESC/P bytes straight to the OS print system. The client
calls `POST /api/vouchers/{id}/print-slip` and paper comes out at the counter.

One interface, two implementations behind build tags — the only place the host
OS leaks into application code:

```go
// internal/printing/raw.go
type RawPrinter interface {
    Print(queue string, data []byte) error
}
```

| Host | Implementation | Setup note |
|---|---|---|
| Windows | `spooler_windows.go` — `alexbrainman/printer`, `RAW` datatype | Printer installed normally |
| macOS | `spooler_unix.go` — CUPS, `lp -d <queue> -o raw` | Add as a raw queue, no driver |
| Linux | `spooler_unix.go` — same CUPS path | `lpadmin -p EPSON -E -v usb://… -m raw` |

CUPS raw queues are a well-trodden path for Epson ESC/P, so the dot-matrix
requirement does **not** constrain the host OS choice.

> ⚠️ **Attach the dot-matrix to the host machine.** If it hangs off a Windows
> counter PC while the host is a Mac or Linux box, the printer must be shared and
> reached over IPP/SMB — doable, but it adds a failure mode for no benefit.

Slip layouts (column positions, form length, condensed-mode codes) live in
config, not code — aligning a carbonised form to a dot-matrix is trial and error
and must never require a rebuild.

If a second counter later needs its own local dot-matrix, the answer is a small
print-agent binary on that PC. Don't build it until it's asked for.

## Decision 5 — Host OS chosen at deploy time

**The host is a build-flag choice, not an architecture choice.** Build all
targets in CI and ship whichever the station ends up using; the same database
file moves between them unchanged, because SQLite files are byte-identical
across platforms.

| Host | Best when | Trade-off |
|---|---|---|
| **Windows** | There's already an always-on office PC | The PC gets used, rebooted, and updated by people; the service must survive that |
| **Linux** (mini-PC or Pi 5) | You want a dedicated appliance | ~₱5–8k for hardware nobody touches, silent, low power. **Most reliable option.** |
| **macOS** | A Mac mini is already sitting there | Fine technically; Gatekeeper needs one extra step |

**Recommendation: a cheap dedicated Linux box if the owner will buy one,
otherwise Windows on the always-on office PC.** The biggest reliability factor
here is not the OS — it's whether the host doubles as somebody's workstation. A
machine someone shuts down at 5pm, or that reboots itself for an update
mid-transaction, will cause more support calls than any technical decision in
this document. A headless appliance removes that entire class of problem.

Three things vary by host, all isolated:

1. **Service manager** — `kardianos/service` targets Windows Services, launchd,
   and systemd through one API.
2. **Data directory** — one `config` function per OS.
3. **Raw printing** — the `RawPrinter` interface above.

Everything else — HTTP, SQLite, the SPA, backups, mDNS — is identical Go with no
platform branches.

---

## System architecture

```
┌───────── HOST MACHINE — Windows, macOS, or Linux (always on) ─────────┐
│                                                                       │
│   bokkie  (one static binary; service: Windows / launchd / systemd)   │
│   ┌─────────────────────────────────────────────────────────────┐     │
│   │  HTTP server  :8080  (stdlib ServeMux, Go 1.22+ routing)    │     │
│   │    ├── /            → embedded Vue SPA (go:embed dist/)     │     │
│   │    ├── /api/*       → JSON API, session-cookie auth         │     │
│   │    └── /print/*     → raw ESC/P via spooler or CUPS         │     │
│   ├─────────────────────────────────────────────────────────────┤     │
│   │  Domain / service layer  (ledger, allocation, numbering)    │     │
│   ├─────────────────────────────────────────────────────────────┤     │
│   │  Storage — SQLite (WAL), sqlc-generated typed queries       │     │
│   ├─────────────────────────────────────────────────────────────┤     │
│   │  Scheduler — nightly VACUUM INTO backup, WAL checkpoint     │     │
│   │  mDNS responder — advertises bokkie.local                   │     │
│   └─────────────────────────────────────────────────────────────┘     │
│                                                                       │
│   Data directory (per-OS — see Operations)                            │
│     bokkie.db · config.yaml · backups/ · logs/ · slips/               │
│                                                                       │
│   [Dot-matrix printer] ← raw ESC/P (spooler | CUPS)                   │
└───────────────────────────┬───────────────────────────────────────────┘
                            │  station LAN / WiFi — no internet required
        ┌───────────────────┼───────────────────┐
        │                   │                   │
   Counter PC          Manager PC          Phone / tablet
   Edge --app=…        Edge --app=…        browser
   (looks native)      + A4 printer        (read-only lookups)
```

### Stack

| Layer | Choice | Why |
|---|---|---|
| Language | Go 1.24+ (1.27 installed) | Single static binary, no runtime |
| HTTP | stdlib `net/http` + `ServeMux` | 1.22 pattern routing is enough; minimal deps |
| DB | SQLite, WAL mode | Local-only by requirement; zero admin |
| Driver | `modernc.org/sqlite` | **Pure Go** — cross-compile with no toolchain |
| Queries | `sqlc` | Typed Go generated from real SQL; no ORM magic |
| Migrations | `pressly/goose`, embedded | Applied automatically on boot |
| Auth | Session cookie + `argon2id` | Local users, no external IdP |
| Service | `kardianos/service` | One API over Windows SCM, launchd, systemd |
| Printing | `alexbrainman/printer` (Win) / CUPS `lp -o raw` (Unix) | Behind one `RawPrinter` interface |
| Discovery | host's own mDNS responder; `grandcat/zeroconf` where there is none | `bokkie.local` survives a DHCP address change |
| Frontend | Vue 3 + Vite + TS | Embedded via `go:embed` |
| UI kit | PrimeVue 4 (Aura) | DataTable handles the grid-heavy screens |
| State | Pinia + vue-router | Standard |
| Tests | Go stdlib `testing`, Vitest, Playwright | Unit → component → a few E2E flows |

### Repository layout

```
bokkie-lms/
├── cmd/bokkie/main.go          # entrypoint; install|uninstall|run|backup subcommands
├── internal/
│   ├── config/                 # config.yaml; datadir_{windows,darwin,linux}.go
│   ├── db/                     # migrations/*.sql, sqlc output, queries/*.sql
│   ├── domain/
│   │   ├── customer/
│   │   ├── advance/            # vouchers + lines
│   │   ├── payment/            # payments + allocation
│   │   ├── ledger/             # ★ append-only entries, balance derivation
│   │   └── interest/           # deferred until the domain interview lands
│   ├── api/                    # handlers, middleware, DTOs
│   ├── printing/               # escp/ templates; raw.go + spooler_{windows,unix}.go
│   ├── backup/                 # scheduled VACUUM INTO + restore
│   └── audit/                  # who did what, when
├── web/                        # Vue SPA (own package.json)
│   └── src/{pages,components,stores,api}
├── docs/plans/                 # these documents
└── Makefile                    # dev, build-all (win/mac/linux × amd64/arm64)
```

Each `internal/domain/*` package owns one concept, exposes a small service
interface, and is testable against an in-memory SQLite without an HTTP server.

---

## Data model

Three principles, each one directly fixing a legacy failure.

**1. Money is `INTEGER` centavos. Never a float.** A `Money` type marshals to
JSON as a decimal *string* so JavaScript's float arithmetic can never touch it.

**2. The ledger is append-only.** The legacy Advances grid had a `Delete` button
that physically removed rows — history simply vanished. Here nothing is ever
deleted or edited after posting; a void writes a *reversing entry*. Every peso
that ever moved stays on the record, which is what you need when a farmer
disputes a balance two seasons later.

**3. Balances are derived, never stored as the source of truth.** The legacy
"Previous Balance / Principal / Interest" fields on the customer record were a
carry-forward hack that drifts out of sync. Here the balance is the sum of the
ledger. `customer_balances` exists only as a cache, rebuildable from scratch at
any time, and a startup self-check verifies it still matches.

```
customers
  id, code (BA-0001, unique), name, address, cell_no,
  active, notes, created_at, updated_at

advance_vouchers                    -- the legacy "Ref. No. 1425" header
  id, ref_no (unique), voucher_date, notes,
  status (draft|posted|voided), posted_at, posted_by, voided_at, voided_by

advance_lines                       -- one voucher can span several customers
  id, voucher_id, customer_id, line_date, particular, amount_cents

payments
  id, or_no, customer_id, payment_date, amount_cents,
  kind (cash|delivery|adjustment),   -- ← delivery support without a migration
  reference, status, created_at, created_by

payment_allocations                 -- which advance a payment paid down
  id, payment_id, advance_line_id,
  principal_applied_cents, interest_applied_cents

ledger_entries                      -- ★ the single source of truth
  id, customer_id, entry_date, entry_type,
      -- advance | interest | payment | adjustment | reversal | opening
  principal_delta_cents, interest_delta_cents,
  source_table, source_id,          -- provenance back to the document
  reverses_id,                      -- set on a reversal, naming its target
  reason,                           -- required on a reversal or an adjustment
  created_at, created_by, created_by_username

customer_balances                   -- derived cache, rebuildable
  customer_id, principal_cents, interest_cents,
  last_activity_date, rebuilt_at

users        id, username, display_name, password_hash, role, active
audit_log    id, at, user_id, action, entity, entity_id, before_json, after_json
settings     key, value             -- ref_no start, interest rules, slip layout
```

The reversal link points **forward**, from the reversal to the entry it undoes,
rather than backward from the target. Writing it on the target would mean
updating a posted row, and then the append-only trigger would need an exception
listing every column that must stay unchanged — a hole that widens silently
every time a later migration adds a column. Pointing forward keeps the trigger
total: `ledger_entries` refuses every `UPDATE`, with no conditions. "Has this
been reversed?" is a lookup on a unique partial index, which is also what makes
*reversed at most once* true under two simultaneous voids rather than merely
usually true.

`created_by_username` sits beside the nullable `created_by` for the same reason
it does in `audit_log`: an interest run or a cutover import happens with nobody
signed in, and inventing a user id for it would make the ledger claim something
it does not know.

**Opening balances** (the start-fresh cutover) are entered as `opening` ledger
entries dated at the cutover, not as columns on `customers`. The ledger stays
pure and the first day's reports are correct.

**Ref. No. continuity:** the starting voucher number is a setting, so you can
continue at 1426 and staff see no discontinuity from the old system.

**Deliveries:** `payments.kind` already carries a `delivery` value. If the owner
confirms farmers repay in palay, delivery detail (kg, price/kg, moisture, class)
becomes a `payment_deliveries` side table — no change to the ledger or to
anything built before it.

### Why this shape survives the unknowns

The interest rule, the repayment mechanism, and the contents of the ROI report
are all still unknown. Every one of them is a *read* over `ledger_entries` or a
*write of new entry types into it*. Flat-rate, monthly-on-outstanding, and
per-cropping-season interest all post the same kind of row and differ only in
what generates it. That is why the ledger goes in first and interest goes in
last — **the schema does not need to know the answer to be correct.**

---

## Operations

**Deployment.** `bokkie install` registers the binary with the host's own service
manager so it starts on boot and restarts on crash — one command, three
platforms:

| Host | Service | Data directory | Runs as |
|---|---|---|---|
| Windows | Windows Service (SCM) | `C:\ProgramData\Bokkie\` | `LocalService` |
| macOS | launchd daemon (`/Library/LaunchDaemons`) | `/Library/Application Support/Bokkie/` | dedicated `_bokkie` user |
| Linux | systemd unit | `/var/lib/bokkie/` | dedicated `bokkie` user |

A `--data-dir` flag overrides all of these — useful for putting the database on
a second disk, and essential for the restore drill.

**Firewall.** Installation opens the port per host: a Windows Firewall rule,
`ufw`/`firewalld` on Linux, an application-firewall exception on macOS (which may
still prompt once on first launch — warn whoever installs it). Forgetting this
is the single most likely "it doesn't work from the other PC" support call, on
any OS.

**macOS gotcha.** An unsigned binary copied onto a Mac is quarantined by
Gatekeeper and refuses to run with an unhelpful error. Fix at build time with an
ad-hoc signature (`codesign -s - bokkie`) or at install time with
`xattr -d com.apple.quarantine bokkie`. Put it in the install script, not in a
README nobody reads.

**mDNS: register through whoever already owns port 5353.** This was learned the
hard way on the first install, and it is not only a Linux problem:

| Host | Who owns mDNS | How bokkie registers |
|---|---|---|
| macOS | `mDNSResponder`, always | `dns-sd -P`, supervised as a child process |
| Linux | Avahi, on any desktop distribution | Avahi's D-Bus API, after the built-in responder fails to bind |
| Windows | usually nothing | the built-in `grandcat/zeroconf` responder |

A second responder on a host that already has one **binds without error and
then answers nothing** — bokkie logs that it is advertising and no phone on the
station can resolve the name. On macOS the Bonjour API is C, and using it would
mean cgo, which would end cross-compilation for every other target: the
supervised `dns-sd` child process is the cheaper trade.

Advertising is a convenience, never a dependency. If it fails, the server still
serves on its IP address and says so in the log.

**Backup — replacing the legacy "Back-up Files" menu item.** Nightly
`VACUUM INTO` writes a single timestamped, consistent `.db` file to a configured
folder (point it at a USB stick or second disk) with an N-copy retention policy.
A "Backup now" button does the same on demand. Restore is: stop service, swap
the file, start service. On a schedule the system also reopens the newest backup
read-only and counts rows — **a backup that has never been verified is not a
backup.**

**Changing host OS uses the same mechanism.** A SQLite file is byte-identical
across platforms, so moving from the Windows office PC to a Linux mini-PC is:
back up, install on the new machine, drop the file in, start. No export step, no
migration tool. That property is what makes Decision 5 genuinely reversible
rather than a commitment.

**Re-indexing no longer exists.** SQLite maintains its own indexes. That menu
item and its entire class of failure disappear.

**Security.** LAN only; the server never makes an outbound connection. Login
required, argon2id hashes, role separation (encoder vs manager) so only a
manager can void a posted voucher. Everything consequential lands in
`audit_log`. Plain HTTP over the station LAN is the pragmatic default — a
self-signed certificate produces alarming browser warnings on phones for little
real gain — but this depends on the WiFi being WPA2 with a password not shared
outside the station. **Raise this with the owner explicitly rather than burying
it.**

**Timezone.** Timestamps stored UTC; business dates stored as `DATE` and always
rendered in `Asia/Manila`.

---

## Roadmap

Steps 1–3 are the architecture and are detailed in their own docs. Steps 4–10
are features — listed for shape only, **each gets its own plan when we reach
it.** Steps 4 through 7 have theirs.

| Step | Scope | Doc | Depends on |
|---|---|---|---|
| 0 | **Domain interview** — questions for the owner | [domain-interview.md](domain-interview.md) | — (parallel track) |
| 1 | **Build & runtime foundation** | [01-foundation.md](01-foundation.md) | — |
| 2 | **Identity, audit, backup** | [02-identity-audit.md](02-identity-audit.md) | Step 1 |
| 2.5 | **Install location, signed USB updates, first inch** | [02.5-install-update.md](02.5-install-update.md) | Step 2 |
| 3 | **Money & ledger core** | [03-ledger-core.md](03-ledger-core.md) | Step 2 |
| 4 | **Customers — CRUD, `BA-####` codes, search, CSV import** | [04-customers.md](04-customers.md) | Step 3 |
| 5 | **Advances — voucher + line grid, draft→posted, void via reversal** | [05-advances.md](05-advances.md) | Step 4 |
| 6 | **Payments — entry, the principal/interest split, customer statement** ✅ | [06-payments.md](06-payments.md) | Step 5 |
| 7 | **Interest — posting run, preview, reversible** ✅ | [07-interest.md](07-interest.md) | Step 6 · Q1 answered by a setting |
| 8 | Reports — Current Advances, ROI, Liquidated, aging; A4 print CSS | *later* | **Blocked on Q3/Q4** |
| 9 | Dot-matrix slips — ESC/P templates, `RawPrinter` wiring | *later* | Step 5 |
| 10 | Cutover — opening balances, parallel run, go-live | *later* | All |

> **Steps 1–3 produce nothing a user can see.** At the end of Step 3 you have a
> binary that installs, logs people in, backs itself up, and can prove arbitrary
> ledger arithmetic is correct — and an empty screen. That is the right order for
> a financial system, but know it going in so the absence of visible progress
> doesn't read as an absence of progress.

---

## Open questions

- **Interest rule, repayment mechanism, ROI report contents** → the domain
  interview. Runs in parallel. It no longer blocks Step 7: the rule is a
  setting whose default charges nothing, and what an answer could still cost is
  recorded in [07-interest.md](07-interest.md). It still blocks Step 8.
- **Whether a second counter needs its own local dot-matrix** → defer; build the
  print agent only if asked.
- **Which host the station actually uses** → blocks nothing. Build all targets
  from task 1.1, decide at deployment, keep it reversible.
- **Whether a Flutter Android client for field collectors is wanted later** →
  the JSON API supports it; revisit after go-live.
