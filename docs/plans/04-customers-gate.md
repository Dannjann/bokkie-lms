# Step 4 — Closing the Gate

**Status:** 🟨 task 1 done · tasks 2 and 3 need the owner ·
**Companion to:** [04-customers.md](04-customers.md)

---

## What this is

Step 4 is built and committed. **Task 1 is done** — the screens have been
walked in a real browser against the real binary, and what it found is recorded
below and in [04-customers.md](04-customers.md). Tasks 2 and 3 are the two
decisions baked into the code that are worth putting in front of the owner
before Step 5 builds on them. Neither blocks Step 5; both should be folded into
the same visit as the [domain interview](domain-interview.md).

This doc is the work order for exactly that. It is written for a session with no
memory of building Step 4, so it repeats what it needs rather than pointing at
the 450-line design doc. Read [04-customers.md](04-customers.md) only if a task
here does not make sense on its own.

**Nothing left here is a known bug.** Tasks 2 and 3 are business questions whose
answers may or may not change a line of code. Do not go looking for something to
fix. Task 1 is kept below as the record of what was actually looked at, so that
"the screens were checked" means something specific.

---

## Where Step 4 stands

Built and on `develop`: commits `f6c5955`, `fffa36f`, `84595fd`, `b989748`,
`885d7d8`. `make ci` passes and all five binaries cross-compile.

| Checked | Result |
|---|---|
| Codes unique, monotonic, never reused over generated sequences | ✅ |
| 50 concurrent creates under `-race` | ✅ 50 distinct codes |
| Allocation past `BA-9999` | ✅ refused, nothing written |
| A bad row at line 300 | ✅ nothing written, the line is named |
| Dry run, both front doors | ✅ table and audit log untouched |
| Searching `%` and `_` | ✅ literal, not a wildcard |
| `0917` finds a number stored as `0917-123-4567` | ✅ |
| An encoder deactivating a customer | ✅ refused, refusal audited |
| A caller supplying a code | ✅ refused at the edge |
| Import end to end from the command line | ✅ one `console` audit row |
| SPA shell and lazy chunks for every new route | ✅ served |
| **The screens, in a browser, at counter-PC resolution** | ✅ **task 1, done** |

---

## Running the app locally

Task 2 needs the real binary, and so did task 1. The Vite dev server is not good
enough for looking at screens — the thing being checked is what the station will
actually run. Kept here because it is the fastest way back to a running system
with data in it, whatever the reason.

```bash
make web-build build-all           # builds the SPA and embeds it in all five binaries
mkdir -p /tmp/bokkie-check
printf 'port: 8099\nmdns:\n  enabled: false\n' > /tmp/bokkie-check/config.yaml
./dist/bokkie-mac-arm64 run -data-dir /tmp/bokkie-check
```

Then open <http://localhost:8099>. The first screen is first-run setup; create an
account and you are in.

Three things that will otherwise cost ten minutes:

- **Use `make web-build build-all`, not `make release`.** `release` ends in a
  signing step that refuses to run on an untagged commit, and this repo has no
  version tag yet. The two targets above produce the same binaries; they just do
  not sign them, which nothing here needs.
- **`bokkie run` has no `-port` flag.** The port comes from `config.yaml` in the
  data directory, which is why the `printf` above is there. `run` takes only
  `-data-dir`, `-console`, `-dev` and `-vite-url`.
- **Port 8080 is the default and is often already taken** on this machine by an
  unrelated dev server. If you skip the config file you will get a confusing
  HTML error page from somebody else's server rather than anything from bokkie.

To get a realistic amount of data in, rather than typing customers by hand:

```bash
cat > /tmp/bokkie-check/customers.csv <<'CSV'
code,name,address,cell_no,active,notes
BA-0001,Juan dela Cruz,Pintor Gamu,0917-123-4567,true,
BA-0002,Maria Santos,Gamu,0918 765 4321,true,seasonal
BA-0003,Pedro Reyes,Naguilian,,false,moved away
BA-0004,100% Palay Trading,Ilagan,0917 000 1111,true,
CSV
./dist/bokkie-mac-arm64 import-customers -data-dir /tmp/bokkie-check -check /tmp/bokkie-check/customers.csv
./dist/bokkie-mac-arm64 import-customers -data-dir /tmp/bokkie-check /tmp/bokkie-check/customers.csv
```

Clean up afterwards with `rm -rf /tmp/bokkie-check`.

---

## Task 1 — Look at the screens ✅

**Done.** The screens were walked in Chromium against the real binary with the
real embedded SPA, signed in as a manager and again as an encoder, at 1280,
1024, 900, 768 and 390px. What follows is what it found; the two design
decisions it produced are recorded in
[04-customers.md](04-customers.md) under *Decisions taken while building*.

The everyday path holds:

- [x] Found a customer by code, by name, and by phone. `0917` finds a number
      entered as `0917-123-4567`, and the list displays it grouped as
      `0917 123 4567` while the database keeps bare digits.
- [x] `%` in the search box returns exactly `100% Palay Trading`, and `_`
      returns nothing. Both are literal, as the server tests already asserted.
- [x] Status filter: Active 6, Deactivated 2, All 8.
- [x] Created a customer. No code appears anywhere on the form before saving;
      `BA-0009` was assigned on save, continuing from `BA-0008`.
- [x] Edited the phone to `(0928) 111-2233` and saved. After a reload the
      stored value is `09281112233`.
- [x] Deactivated and reactivated as a manager.
- [x] As an encoder: no Deactivate button on the detail screen, no Import CSV
      button on the list, no "Import customers" in the rail, and typing
      `/customers/import` redirects to `/customers`. Save changes stays
      available, which is the counter work encoders do all day.
- [x] Import: a good file reports what it would write and writes nothing; a
      file with a bad row names the line — *line 3, "maybe" is not a yes or a
      no in the active column* — and refuses the whole file.

And the judgement calls:

- [x] **Counter-PC width.** This is where the one real finding was. The page
      header's buttons stacked on top of each other in a band from 964px to
      1032px and nowhere else — the station's `--app` window at ~1024px sits in
      the middle of it. Fixed, and the reasoning is in 04-customers.md; the
      band was then swept again at every width from 700 to 1440px.
- [x] **No screen scrolls sideways** at any of the five widths, signed in or
      out, with a rejection table on screen or without one. The customer table
      scrolls inside its own container on a phone rather than pushing the page.
- [x] **The import rejection table** reads correctly at 390px. The code column
      wraps `BA-0102` across two lines, which is ugly and was left alone —
      importing a CSV is not a phone job, and the table is legible.

**Not covered by this, and still worth doing when the hardware exists:** a real
phone on the station LAN, rather than a 390px viewport with touch emulation.
That is the same drill Step 1's gate is waiting on — see
[01-foundation.md](01-foundation.md) — and it belongs to that trip, not this
one.

**Two cosmetic things noticed and deliberately not fixed**, recorded so the next
person does not spend time rediscovering them:

- The import screen prints the file's SHA-256 under the filename with no label
  saying what the hex is. It is right that it is on screen and not only in the
  audit log; it would read better with a word in front of it.
- A disabled PrimeVue primary button is the full brand colour at 60% opacity,
  which still looks pressable. The Import button is correctly disabled until a
  check passes — verified, not assumed — but a glance would not tell you.

---

## Task 2 — Confirm the CSV import is strict enough, and not too strict

**The decision:** an unrecognised column in the CSV header fails the whole file,
rather than being ignored.

`internal/domain/customer/import.go:238`, with the known set at line 40:
`code, name, address, cell_no, active, notes`.

**Why it was built that way:** a header reading `cellno` where `cell_no` was
meant would otherwise import all 364 customers with no phone numbers at all, and
look like it had worked. That is discovered weeks later by somebody trying to
ring a farmer.

**The risk:** if the legacy DBF export carries columns we do not care about —
and a FoxPro export very well might — every import fails until somebody deletes
those columns in Excel first. At the station, on cutover morning, that is a bad
moment to discover it.

**What to do:** get a real export, or a sample of one, and run
`bokkie import-customers -check` against it. The dry run writes nothing and
lists every problem at once, so this costs one command.

If it turns out the export carries extra columns:

- Do **not** silently ignore unknown columns. That reintroduces exactly the
  failure the rule exists to prevent.
- Prefer keeping the error and documenting the two-minute Excel fix in the
  runbook.
- If that is genuinely unworkable, the shape to add is an explicit
  `-ignore-unknown-columns` flag — visible, opt-in, and named so that whoever
  passes it knows what they are giving up.

**Done when:** a real legacy export has been through `-check`, and either it
passes or a deliberate decision has been recorded here.

---

## Task 3 — Confirm who may do what

**The decision:** encoders can create and edit customers; only managers can
deactivate one or run an import.

`internal/api/api.go:172-193`. Changing any of it is one word per line —
`Authenticated` or `ManagerOnly` — and the route registry is the only place it
is expressed, which is exactly why it is safe to change.

| Route | Today | The reasoning |
|---|---|---|
| `GET /api/customers` | Authenticated | encoders look people up all day |
| `GET /api/customers/{id}` | Authenticated | |
| `POST /api/customers` | Authenticated | adding a farmer at the counter is the everyday work |
| `PUT /api/customers/{id}` | Authenticated | fixing a phone number is counter work |
| `POST /api/customers/{id}/active` | **ManagerOnly** | the nearest thing to deletion |
| `POST /api/customers/import` | **ManagerOnly** | |

**Ask the owner, and ask the encoder separately** — the domain interview already
notes the encoder often knows things the owner does not, especially about
exceptions.

The question worth asking plainly: *if a farmer is standing at the counter and
is not on the system, should the person at the counter be able to add them, or
should that wait for a manager?* Everything else follows from the answer.

**If it changes:** update `internal/api/api.go`, then
`internal/api/customers_test.go` — `TestCustomerRoutesAreRegisteredWithTheRightAccess`
asserts this exact table and will fail until it is updated, which is the point
of it. Also update the table in [04-customers.md](04-customers.md) at line 375
and the one above.

**Done when:** the table above has been confirmed or amended by somebody who runs
the business.

---

## When the other two are done

Step 4's gate is closed — that happened with task 1, and both
[04-customers.md](04-customers.md) and [README.md](README.md) say so. What is
left is bookkeeping on two answers from the owner:

- Record each answer in 04-customers.md under *Decisions taken while building*,
  whether or not it changed a line of code. "We asked, and it stays as it is"
  is worth as much there as an amendment, because it stops the question being
  reopened from scratch in six months.
- Then delete this file. It is a work order, not a record, and by that point
  everything in it that outlives the tasks will be in 04-customers.md, where
  the next person will actually look for it.

---

## What this does not cover

These are outstanding, tracked in their own docs, and **not** part of closing
Step 4's gate:

- **Step 1's manual drill** — a second machine and a phone reaching
  `bokkie.local` across a LAN, plus a reboot. Needs hardware this project has
  not had. See [01-foundation.md](01-foundation.md).
- **`make keygen` and a first version tag** — both required before the first
  release can be built or an update applied. See
  [02.5-install-update.md](02.5-install-update.md).
- **The domain interview** — blocks Steps 7 and 8, not this. See
  [domain-interview.md](domain-interview.md). Tasks 2 and 3 above are worth
  folding into that same visit rather than making a separate trip.

Do not start Step 5. It gets its own plan, written when we reach it.
