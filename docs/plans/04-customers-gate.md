# Step 4 — Closing the Gate

**Status:** ⬜ not started · **Companion to:** [04-customers.md](04-customers.md)

---

## What this is

Step 4 is built and committed. Every line of its gate has been checked except
one, and two decisions baked into the code are worth putting in front of the
owner before Step 5 builds on them.

This doc is the work order for exactly that. It is written for a session with no
memory of building Step 4, so it repeats what it needs rather than pointing at
the 450-line design doc. Read [04-customers.md](04-customers.md) only if a task
here does not make sense on its own.

**Nothing here is a known bug.** Task 1 is a verification that has not been
performed; tasks 2 and 3 are business questions whose answers may or may not
change a line of code. Do not go looking for something to fix.

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
| **The screens, in a browser, at counter-PC resolution** | ⬜ **task 1** |

---

## Running the app locally

Both tasks below need the real binary with the real embedded SPA. The Vite dev
server is not good enough for task 1 — the thing being checked is what the
station will actually run.

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

## Task 1 — Look at the screens

**The only outstanding line of Step 4's gate.** Everything reachable without a
browser has been verified: the shell and every lazy chunk serve for all four
routes, and the whole flow works end to end over real HTTP against the real
binary. What is unverified is purely how it renders.

The four screens are `/customers`, `/customers/new`, `/customers/:id` and
`/customers/import` (the last is manager-only and an encoder is redirected away
from it).

Walk the everyday path and check it holds together:

- [ ] Find a customer by code, by name, and by phone. Typing `0917` must find a
      number displayed as `09171234567`.
- [ ] Type `%` into the search box. It must match `100% Palay Trading` and
      nothing else — not the whole master.
- [ ] Switch the status filter through Active / Deactivated / All.
- [ ] Create a customer. The code must appear only after saving, and must be the
      next one in sequence.
- [ ] Edit a phone number with punctuation in it and save. It should come back
      stripped to digits.
- [ ] Deactivate and reactivate as a manager.
- [ ] Sign in as an encoder and confirm the Deactivate button and the Import CSV
      button are both absent.
- [ ] On the import screen, Check a good file and a file with a bad row. The
      rejection table must name the line.

**Then the part that actually needs a person**, because it is a judgement rather
than an assertion:

- [ ] **Counter-PC width.** The station runs Edge in `--app` mode, so there is no
      address bar and the window may be narrower than a browser window. Narrow
      the window to roughly 1024px and check the DataTable does not spill and the
      header does not wrap into something silly.
- [ ] **A phone.** The requirement that decided the whole architecture was
      "several PCs plus phones/tablets". Open it on one. The list and the search
      box are what matter; the import screen is not a phone job.
- [ ] **The import rejection table** at narrow width. It has three columns and
      the reason column carries a full sentence, which is the most likely thing
      on any of these screens to lay out badly.

**Done when:** a customer can be found, created, edited and deactivated from a
browser, and nothing is unusable at 1024px or on a phone.

If something is wrong, it is almost certainly CSS in the `<style scoped>` block
of the page concerned — the data and the API behind these screens are already
covered by tests.

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

## When all three are done

- Mark Step 4 ✅ in [04-customers.md](04-customers.md) and in
  [README.md](README.md).
- Delete this file. It is a work order, not a record — what it decided belongs
  in 04-customers.md under *Decisions taken while building*, where the next
  person will actually look for it.

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
