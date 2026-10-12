# Step 2 — Identity, Audit, and Safety Rails

**Status:** ✅ built, gate passed · **Depends on:** Step 1 · **Blocks:** Step 3 onward

---

## Goal

Still no lending domain. This step builds the things that must exist *before*
anything touches money: **who did it**, and **how we get the data back**.

The ordering is deliberate. Audit logging retrofitted after the fact is always
half-hearted — some handlers get it, some don't, and nobody can tell which. A
backup system written after go-live gets written in a hurry, under pressure,
after the first scare. Both belong here, while there is nothing to lose yet and
the patterns can be enforced from the first mutating handler onward.

## Done when

- Two roles behave differently, and the refusal is recorded.
- Every mutation lands in `audit_log`.
- A nightly backup exists **and has been restored** into a scratch data dir with
  matching row counts.

---

## Tasks

### 2.1 — Users and authentication

- `users` table: id, username, display_name, password_hash, role, active,
  created_at, last_login_at.
- **argon2id** password hashing with tuned parameters. Not bcrypt, not SHA-any.
- Session cookies: `HttpOnly`, `SameSite=Lax`, server-side session store in
  SQLite so sessions survive a restart. Staff being logged out every time the
  service restarts would be a daily irritation.
- Login, logout, and a **first-run admin setup** flow: on an empty `users` table
  the app serves a one-time create-admin page instead of a login form, then
  never again.
- Rate-limit failed logins per username. A station PC on a shared LAN with a
  four-digit-PIN culture needs at least this much.

Consider session lifetime carefully: a counter PC left logged in all day is the
realistic usage, so a short idle timeout will be hated and worked around
(shared passwords on sticky notes). Prefer a long session plus an explicit lock
action.

**Done when:** first-run setup creates an admin, login works from another
machine, sessions survive a service restart.

---

### 2.2 — Roles and authorization

- Two roles: `encoder` and `manager`. Resist adding more until something
  actually needs them.
- Authorization middleware, applied at route registration rather than checked
  inside handlers — it must be impossible to add a route and forget the check.
- **Manager-only is what later gates voiding a posted voucher** (Step 5), which
  is the single most sensitive action in the system. Build the gate now so that
  feature has somewhere to attach.
- A refused request is a security event: log it to `audit_log`, not just the
  request log.

**Done when:** an encoder is refused a manager-only route, and the refusal
appears in `audit_log`.

---

### 2.3 — Audit log

- `audit_log`: id, at, user_id, action, entity, entity_id, before_json,
  after_json, request_id, ip.
- A single helper that every mutating handler calls. **Make the easy path the
  audited path** — if writing an audited mutation is more work than an
  unaudited one, some handler will eventually skip it.
- Append-only. No update or delete path, in code or in SQL.
- Include failed authorization attempts and login failures, not only successful
  mutations.

Retention: never auto-purge. This is a financial record for a lending business;
the table is small and the value of old entries is exactly when someone disputes
something old.

**Done when:** every mutating route writes an entry, verified by a test that
enumerates registered mutating routes and asserts coverage rather than trusting
each handler individually.

---

### 2.4 — Backup subsystem

This replaces the legacy *Back-up Files* menu item, and it is the single
highest-value thing in Step 2.

- Scheduled `VACUUM INTO` producing one timestamped, internally consistent `.db`
  file. (`VACUUM INTO` over file copying: a copy of a live WAL database can be
  torn.)
- Configurable destination — point it at a USB stick or second disk — with
  N-copy retention.
- **Automated verification:** after each backup, reopen the new file read-only
  and count rows in key tables. A backup that has never been verified is not a
  backup; it is a folder of files you *hope* are backups.
- Manual "Backup now" trigger, exposed as `bokkie backup` on the CLI and later
  as a button.
- Documented restore: stop service, swap file, start service. Write it down in
  `docs/runbook.md` in language the owner could follow if you are unreachable.

**Done when:** a nightly backup runs, verifies itself, and has been **actually
restored** into a scratch `--data-dir` with matching row counts. Perform the
restore — do not just implement it.

---

### 2.5 — Settings table

- `settings` as typed key/value: Ref. No. start value, timezone, backup
  schedule, retention count, printer queue, slip layout parameters.
- Read through a small typed accessor, not raw string lookups scattered around.
- Seeded with defaults on first run.

The test for what belongs here: **would changing this require a rebuild?** Slip
column positions must be adjustable while standing at a dot-matrix printer with
a misaligned form. Ref. No. start must be settable at cutover so numbering
continues from 1426 and staff notice no discontinuity.

**Done when:** settings are readable and writable, seeded on first run, and a
changed setting takes effect without a restart.

---

## Verification

```bash
go test ./...    # auth + role middleware against in-memory SQLite, no HTTP
```

- An encoder is refused a manager-only route; the refusal is in `audit_log`.
- Route-coverage test: every registered mutating route produces an audit entry.
- Backup runs, self-verifies, and restores into a scratch `--data-dir` with
  matching row counts.
- Session survives a service restart.
- First-run setup cannot be re-triggered once an admin exists.

## Risks

| Risk | Mitigation |
|---|---|
| Audit logging skipped on some handler | Route-enumeration coverage test, not per-handler discipline |
| Backups exist but were never restorable | Automated verify + a real restore drill in this step |
| Short session timeout drives password sharing | Long session + explicit lock action |
| Roles multiply prematurely | Two roles only until a feature genuinely needs a third |


---

## What was built

All five tasks are in, and the gate in **Done when** has been run rather than
reasoned about. `make ci` passes: five binaries, `CGO_ENABLED=0`, `go vet` per
GOOS, and the Go suite green.

### Packages

| Package | What it owns |
|---|---|
| `internal/auth` | argon2id hashing, users, SQLite-backed sessions, the failed-login limiter |
| `internal/audit` | the append-only writer, and `RecordTx` for atomicity with a change |
| `internal/settings` | typed key/value with a read-through cache |
| `internal/backup` | `VACUUM INTO`, verification, retention, the nightly scheduler |
| `internal/api/routes.go` | the registry that makes access and audit structural |

### The route registry

The plan asked for authorization "applied at route registration rather than
checked inside handlers", and for audit coverage proved by enumeration. Both are
the same mechanism: every route is registered through a small registry that
records its access level and how it is audited.

```go
rt.read(ManagerOnly, "/api/users", handleListUsers(d))
rt.mutate(ManagerOnly, http.MethodPost, "/api/users", "user.created", "user", handleCreateUser(d))
```

`Access` is a required argument, so a route with no access decision does not
compile. A mutating route must be registered through one of three functions,
each of which writes the audit entry or records why the handler does it instead:

- **`mutate`** — the default. Runs the handler in a transaction and writes the
  audit entry *into that same transaction*, so a change without its trail, and a
  trail for a change that rolled back, are both impossible.
- **`mutateOutsideTx`** — for operations SQLite refuses inside a transaction.
  Only `VACUUM INTO` needs it.
- **`mutateSelfAudited`** — for the auth handlers, which write richer entries
  than a wrapper could. It takes a written reason, which `routes_test.go` prints
  and requires to be non-trivial, so the exception stays visible.

`TestEveryMutatingRouteIsAudited` walks the registry rather than trusting any of
this. `TestOnlyTheLoginFlowIsPublic` pins the public set, so a route that becomes
public by copy-paste fails instead of shipping.

### Decisions worth keeping

**Sessions are 30 days with no idle timeout, plus an explicit Lock.** The
realistic usage is a counter PC signed in from opening to closing. A short idle
timeout does not make that safer; it produces shared passwords on sticky notes.
Lock ends the session but leaves the username on the login form, so stepping away
costs one click and coming back costs a password. Physical control of the station
is the real boundary, and it is a better one than a timer.

**The session cookie is stored as a SHA-256 hash.** A copy of `bokkie.db`
therefore contains nothing replayable as a login — which matters precisely
because this step starts writing copies of it onto a USB stick every night.

**The rate limiter is in memory, per username.** Per username, not per IP,
because the whole station shares one router and IP-based locking would let one
person's typing lock out the counter. In memory because a restart clearing the
lockouts is a *feature* — it gives the owner a way to unlock someone without a
SQL shell, and an attacker cannot restart the service.

**`audit_log` is append-only in the schema, not just in the code.** Triggers
refuse `UPDATE` and `DELETE`. Code that never writes an update is one careless
commit away from writing one, and the realistic way an audit trail actually gets
edited is someone typing SQL into a `sqlite3` shell. The `user_id` foreign key
also means an account with history can never be deleted, only deactivated —
which is the correct constraint for a financial record.

**Failed logins record *which* failure; the response never does.** "No such user"
and "wrong password" are different stories when read months later, and identical
when handed to someone guessing. `Authenticate` also hashes a dummy password on
an unknown username so the two paths take the same time.

## Deviations from the plan as written

**`audit_log` gained a `username` column.** The plan's schema has `user_id`
only. A failed login for a username that does not exist has no user to point at,
and "someone tried to log in as `admin` forty times" is exactly the entry worth
keeping — so the username is denormalised alongside the id.

**`config.yaml` lost `timezone` and `backup.retention`.** Task 2.5's test —
"would changing this require a rebuild?" — applies just as well to the config
file, and both of these are business decisions rather than machine facts. They
now live in `settings`, where a manager can change them and the change takes
effect immediately. `backup.folder` stays in `config.yaml`: it is a path on one
particular machine, usually a USB stick, and a path stored in the database would
be wrong the moment that database is restored onto a different host. Both keys
are now *rejected* by the strict config parser, so an old file fails loudly
rather than being silently ignored.

**Backup retention is enforced, but not the "N copies per destination" the plan
implies.** There is one destination and one retention count. A second
destination is a config change away and no feature has asked for it yet.

## Verification — what was actually run

`go test ./...` covers the unit and handler level: role refusal and its audit
entry, route coverage, rate limiting, session survival across a pool restart,
setup-once, append-only triggers, retention order, and a backup→restore
round trip in `TestABackupCanActuallyBeRestored`.

Beyond that, the full drill was run against a real binary (`dist/bokkie-mac-arm64`)
on a scratch `--data-dir`, over HTTP:

| Checked | Result |
|---|---|
| Fresh install reports `needs_setup` | ✅ |
| First-run setup creates a manager and signs them in | ✅ |
| Second setup attempt | ✅ 409, one user remains, `setup.refused` in the log |
| Manager creates an encoder | ✅ |
| Encoder → `GET /api/users` | ✅ 403, `auth.access.denied` recorded with the route |
| Encoder → `PUT /api/settings` | ✅ 403 and recorded |
| Encoder → `GET /api/settings` | ✅ 200 — read is authenticated, write is manager |
| Anonymous → `/api/settings` | ✅ 401 |
| Six wrong passwords | ✅ 401×5 then 429 "try again in 15 minutes", all five failures plus the lockout in the log |
| Manager sets `backup.retention` to 3 | ✅ entry carries `{"value":"14"} → {"value":"3"}` |
| A 4th backup with retention 3 | ✅ oldest pruned — the setting took effect with no restart |
| `bokkie backup` against the *running* server | ✅ written and verified |
| `bokkie backup -verify <file>` | ✅ counts printed |
| **Restore drill** | stop service → delete `bokkie.db`, `-wal`, `-shm` → copy backup in → start |
| …`/healthz` after restore | ✅ `"database":"ok"` |
| …row counts | ✅ users 2, audit_log 15, settings 5 — matching the backup |
| …the changed setting | ✅ `backup.retention` still 3 |
| …the pre-disaster session cookie | ✅ still signed in — sessions survived the restore |
| …append-only triggers | ✅ hand-typed `DELETE` and `UPDATE` both refused |
| …role separation | ✅ encoder still 403 |
| Graceful shutdown | ✅ `shutting down` → `stopped` in the log, both times |

`docs/runbook.md` documents the restore in language the owner can follow.

## Left for later

- **The restore drill was run on macOS only.** Windows and Linux hosts remain
  unavailable, so the service-management commands in the runbook for those two
  are derived from the service name rather than exercised. This is the same
  caveat step 1 carries, and it travels to step 10.
- **No frontend tests.** The Vue side is three screens (setup, login, lock) with
  a router guard; `vue-tsc` type-checks them in `make ci`, but Vitest is not set
  up. It arrives with the first screen that has logic worth testing.
- **No password change or reset.** A manager can create users but not change
  their password, and there is no recovery path if every manager account is lost.
  Worth adding before cutover.
- **`mutateOutsideTx` has a real window.** If the audit write fails after a
  successful backup, the operation happened and the entry did not. It is reported
  as a 500 and logged at error level, but it cannot be undone. Only the backup
  route uses this path today.
