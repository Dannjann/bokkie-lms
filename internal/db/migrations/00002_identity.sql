-- Step 2 — identity, audit and settings.
--
-- Timestamps are TEXT in RFC3339 UTC rather than integers. This is a financial
-- record that someone will eventually open in a sqlite3 shell years from now,
-- possibly not us, and "2026-09-15T02:31:07.412Z" needs no explanation where
-- 1789431067 does. Rendering into the station's timezone is presentation.

-- +goose Up

CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    display_name  TEXT    NOT NULL,
    password_hash TEXT    NOT NULL,
    role          TEXT    NOT NULL CHECK (role IN ('encoder', 'manager')),
    active        INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    created_at    TEXT    NOT NULL,
    last_login_at TEXT
);

-- Sessions live in the database, not in memory: staff being logged out every
-- time the service restarts would be a daily irritation, and a restart is how
-- every upgrade lands.
--
-- id is the SHA-256 of the cookie token, never the token itself. A stolen copy
-- of bokkie.db then cannot be turned into a live session, which matters because
-- the backup files this same step writes get carried around on a USB stick.
CREATE TABLE sessions (
    id           TEXT    PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at   TEXT    NOT NULL,
    expires_at   TEXT    NOT NULL,
    last_seen_at TEXT    NOT NULL,
    ip           TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX idx_sessions_user    ON sessions(user_id);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);

-- username is denormalised alongside user_id on purpose. A failed login for a
-- username that does not exist has no user_id to record, and "someone tried to
-- log in as 'admin' forty times" is exactly the entry worth keeping.
CREATE TABLE audit_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    at          TEXT    NOT NULL,
    user_id     INTEGER REFERENCES users(id),
    username    TEXT    NOT NULL DEFAULT '',
    action      TEXT    NOT NULL,
    entity      TEXT    NOT NULL DEFAULT '',
    entity_id   TEXT    NOT NULL DEFAULT '',
    before_json TEXT,
    after_json  TEXT,
    request_id  TEXT    NOT NULL DEFAULT '',
    ip          TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX idx_audit_at     ON audit_log(at);
CREATE INDEX idx_audit_user   ON audit_log(user_id, at);
CREATE INDEX idx_audit_entity ON audit_log(entity, entity_id, at);

-- Append-only, enforced by the database rather than by everyone remembering.
-- Code that never writes an UPDATE is one careless commit away from writing
-- one; a trigger is not. This also stops a hand-typed UPDATE in a sqlite3
-- shell, which is the realistic way an audit trail actually gets edited.

-- +goose StatementBegin
CREATE TRIGGER audit_log_is_append_only_update
BEFORE UPDATE ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit_log is append-only: entries cannot be updated');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER audit_log_is_append_only_delete
BEFORE DELETE ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit_log is append-only: entries cannot be deleted');
END;
-- +goose StatementEnd

-- Everything whose change must not require a rebuild. The test for what belongs
-- here is exactly that: slip column positions have to be adjustable while
-- standing at a misaligned dot-matrix printer, and the Ref. No. start has to be
-- settable at cutover so numbering continues from the legacy system's last one.
--
-- Defaults are seeded from Go rather than inserted here, so the default and the
-- type that parses it stay in one place: internal/settings/defs.go.
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- +goose Down

DROP TABLE settings;
DROP TRIGGER audit_log_is_append_only_delete;
DROP TRIGGER audit_log_is_append_only_update;
DROP TABLE audit_log;
DROP TABLE sessions;
DROP TABLE users;
