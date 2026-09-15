# Step 2 — Identity, Audit, and Safety Rails

**Status:** not started · **Depends on:** Step 1 · **Blocks:** Step 3 onward

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
