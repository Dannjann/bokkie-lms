-- Step 3 — the ledger spine.
--
-- Everything from step 4 onward is a read or a write against this file. It is
-- also the part of the system most likely to outlive the people who wrote it:
-- the question it exists to answer is "what did this farmer owe in March two
-- seasons ago", asked by someone holding a paper slip and expecting the screen
-- to agree with it.
--
-- The constraints below are therefore in the database rather than only in Go.
-- Go code is one careless commit from writing an UPDATE; a trigger is not. And
-- the realistic way a financial record actually gets edited is not through the
-- application at all — it is somebody opening bokkie.db in a SQLite browser
-- because they were sure about what the number should have been.

-- +goose Up

-- Customers arrive here rather than in step 4 for one reason: ledger_entries
-- needs a real foreign key, and SQLite cannot add one to an existing table
-- without rebuilding it — which, on a table wrapped in append-only triggers,
-- means dropping the very protection that makes the table trustworthy.
--
-- This is the shape from 00-architecture.md and nothing more. The domain
-- package, the service and the screens are step 4's; what exists today is a
-- table for the ledger to point at.
CREATE TABLE customers (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    code       TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    name       TEXT    NOT NULL,
    address    TEXT    NOT NULL DEFAULT '',
    cell_no    TEXT    NOT NULL DEFAULT '',
    active     INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    notes      TEXT    NOT NULL DEFAULT '',
    created_at TEXT    NOT NULL,
    updated_at TEXT    NOT NULL
);

-- The single source of truth. Every balance in the system is a SUM over this
-- table; nothing else is authoritative, ever.
--
-- Principal and interest are carried in separate columns and are never netted
-- against each other. The legacy customer record kept them apart, which tells
-- us the business thinks of them apart — and the question of which one a
-- payment clears first only has meaning while they stay distinct.
--
-- Deltas are signed from the lender's point of view: positive increases what
-- the customer owes. A payment is therefore negative, and a balance is the
-- plain sum of the column with no case analysis anywhere.
CREATE TABLE ledger_entries (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    customer_id           INTEGER NOT NULL REFERENCES customers(id),

    -- The business date, in station-local time: the day the money moved, which
    -- is what a farmer's slip says and what a statement is cut on. Deliberately
    -- a date and not a timestamp, and deliberately separate from created_at,
    -- which is the day the row was typed.
    entry_date            TEXT    NOT NULL CHECK (entry_date LIKE '____-__-__'),

    entry_type            TEXT    NOT NULL CHECK (entry_type IN
                              ('opening', 'advance', 'interest', 'payment', 'adjustment', 'reversal')),

    principal_delta_cents INTEGER NOT NULL,
    interest_delta_cents  INTEGER NOT NULL,

    -- Provenance back to the document a person can be shown. A voucher line, a
    -- payment, an interest run. Empty for entries that have no document behind
    -- them, which today is only the cutover's opening balances.
    source_table          TEXT    NOT NULL DEFAULT '',
    source_id             INTEGER,

    -- Set on a reversal, naming the entry it undoes. The link points forward
    -- from the reversal rather than backward from its target, because writing
    -- it on the target would mean updating a posted row — and then the
    -- append-only trigger below would need an exception, which is a hole that
    -- grows every time somebody adds a column and forgets to mention it.
    reverses_id           INTEGER REFERENCES ledger_entries(id),

    -- Why, in the words of whoever did it. Required for the two entry types
    -- that exist because something went wrong.
    reason                TEXT    NOT NULL DEFAULT '',

    created_at            TEXT    NOT NULL,

    -- user_id is nullable and the username is not, for the same reason as in
    -- audit_log: the interest run and the cutover import happen with nobody
    -- signed in, and inventing a user id for them would make the ledger claim
    -- something it does not know. "console" is a truthful answer to "who".
    created_by            INTEGER REFERENCES users(id),
    created_by_username   TEXT    NOT NULL CHECK (length(trim(created_by_username)) > 0),

    -- An entry that moves no money is not an entry; it is a mistake that would
    -- sit in a statement forever looking like it meant something.
    CHECK (principal_delta_cents <> 0 OR interest_delta_cents <> 0),

    -- The sign rules from the plan's entry-type table, made structural. An
    -- advance that reduces a balance, or a payment that increases one, is a bug
    -- in whatever wrote it and should never reach the file.
    CHECK (entry_type <> 'advance'  OR (principal_delta_cents > 0 AND interest_delta_cents = 0)),
    CHECK (entry_type <> 'interest' OR (principal_delta_cents = 0 AND interest_delta_cents > 0)),
    CHECK (entry_type <> 'payment'  OR (principal_delta_cents <= 0 AND interest_delta_cents <= 0)),
    -- opening, adjustment and reversal are deliberately unconstrained in sign:
    -- a customer can start in credit, a correction goes either way, and a
    -- reversal's signs are fixed by its target rather than by its type.

    -- reverses_id and the reversal type imply each other exactly.
    CHECK (entry_type <> 'reversal' OR reverses_id IS NOT NULL),
    CHECK (entry_type =  'reversal' OR reverses_id IS NULL),

    -- Undoing something and adjusting something are the two acts that need
    -- explaining later. Requiring the sentence at the point of writing is the
    -- only moment anyone still knows it.
    CHECK (entry_type NOT IN ('reversal', 'adjustment') OR length(trim(reason)) > 0),

    -- Provenance is either complete or absent; half of it is a dangling
    -- reference that looks like a real one.
    CHECK ((source_table =  '' AND source_id IS NULL)
        OR (source_table <> '' AND source_id IS NOT NULL))
);

-- Statements and as-of balances both walk one customer in date order.
CREATE INDEX idx_ledger_customer_date ON ledger_entries(customer_id, entry_date);

-- "Show me the ledger entries this voucher produced", which is how a posted
-- document is traced and how a void finds what it has to undo.
CREATE INDEX idx_ledger_source ON ledger_entries(source_table, source_id)
    WHERE source_id IS NOT NULL;

-- An entry can be reversed at most once, enforced here rather than by a Go
-- check that races with itself under two simultaneous voids.
CREATE UNIQUE INDEX idx_ledger_reversed_once ON ledger_entries(reverses_id)
    WHERE reverses_id IS NOT NULL;

-- Append-only, enforced by the database.
--
-- There is no exception and no WHEN clause. That is the point: a total trigger
-- stays correct when a column is added in a later migration, and a conditional
-- one silently stops covering it.

-- +goose StatementBegin
CREATE TRIGGER ledger_entries_is_append_only_update
BEFORE UPDATE ON ledger_entries
BEGIN
    SELECT RAISE(ABORT, 'ledger_entries is append-only: post a reversing entry instead of editing one');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER ledger_entries_is_append_only_delete
BEFORE DELETE ON ledger_entries
BEGIN
    SELECT RAISE(ABORT, 'ledger_entries is append-only: post a reversing entry instead of deleting one');
END;
-- +goose StatementEnd

-- What a reversal is allowed to be. Go enforces all of this too, but these are
-- the rules whose violation is unrecoverable, and the ledger should be
-- defensible even against a row inserted by hand.

-- +goose StatementBegin
CREATE TRIGGER ledger_entries_reversal_is_well_formed
BEFORE INSERT ON ledger_entries
WHEN NEW.reverses_id IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'a reversal must name an entry that exists')
    WHERE NOT EXISTS (SELECT 1 FROM ledger_entries WHERE id = NEW.reverses_id);

    -- Correcting a void means posting a fresh entry, which leaves a legible
    -- trail of what actually happened. A chain of reversals of reversals
    -- leaves a puzzle.
    SELECT RAISE(ABORT, 'a reversal cannot itself be reversed: post a fresh entry instead')
    WHERE (SELECT entry_type FROM ledger_entries WHERE id = NEW.reverses_id) = 'reversal';

    SELECT RAISE(ABORT, 'a reversal must belong to the same customer as the entry it reverses')
    WHERE NEW.customer_id <> (SELECT customer_id FROM ledger_entries WHERE id = NEW.reverses_id);

    -- Exactly the negation, to the centavo. "Close enough" is how a balance
    -- drifts by an amount nobody can later account for.
    SELECT RAISE(ABORT, 'a reversal must be the exact negation of the entry it reverses')
    WHERE NEW.principal_delta_cents
              <> -(SELECT principal_delta_cents FROM ledger_entries WHERE id = NEW.reverses_id)
       OR NEW.interest_delta_cents
              <> -(SELECT interest_delta_cents  FROM ledger_entries WHERE id = NEW.reverses_id);
END;
-- +goose StatementEnd

-- A cache and nothing else. Every column here is recomputable from
-- ledger_entries by summing, which is why this table is the one place in the
-- ledger that is freely updatable and freely deletable — losing it costs a
-- rebuild, not a record.
--
-- The legacy system's carry-forward balance fields were the same numbers
-- treated as the truth, and they drifted. The difference is not the schema, it
-- is that here the sum is authoritative and this is checked against it.
CREATE TABLE customer_balances (
    customer_id        INTEGER PRIMARY KEY REFERENCES customers(id) ON DELETE CASCADE,
    principal_cents    INTEGER NOT NULL DEFAULT 0,
    interest_cents     INTEGER NOT NULL DEFAULT 0,
    last_activity_date TEXT,
    rebuilt_at         TEXT    NOT NULL
);

-- +goose Down

DROP TABLE customer_balances;
DROP TRIGGER ledger_entries_reversal_is_well_formed;
DROP TRIGGER ledger_entries_is_append_only_delete;
DROP TRIGGER ledger_entries_is_append_only_update;
DROP TABLE ledger_entries;
DROP TABLE customers;
