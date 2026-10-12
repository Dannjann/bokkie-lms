-- Step 5 — the advance voucher.
--
-- The first document in the system that puts entries into the ledger. It is
-- the legacy "Ref. No. 1425" header with its grid of lines beneath it: a
-- voucher is typed as a draft, posted once, and undone only by reversing every
-- line at once.
--
-- The rules that matter here are about *when* a row may change. A draft is
-- meant to be edited — that is what a draft is for. The moment it is posted it
-- describes money that has moved, and from then on it is as fixed as the ledger
-- entries it produced. That line is drawn by the status column, so the triggers
-- below key off OLD.status rather than off a list of columns, and keep covering
-- any column a later migration adds.

-- +goose Up

-- One voucher header.
--
-- ref_no is NULL until the voucher is posted. Numbering a draft would mean an
-- abandoned draft leaves a permanent hole in a sequence staff read as a run —
-- and at cutover this sequence continues the legacy one, where a gap is a
-- question somebody will ask. It is an INTEGER because the legacy numbers run
-- into the 1400s and are read and said aloud as plain numbers.
CREATE TABLE advance_vouchers (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,

    ref_no       INTEGER CHECK (ref_no IS NULL OR ref_no > 0),

    -- The business date of the voucher, in station-local time, in the same
    -- shape as a ledger entry's. A line may carry its own date; this is the
    -- one the grid starts each line from.
    voucher_date TEXT    NOT NULL CHECK (voucher_date LIKE '____-__-__'),

    notes        TEXT    NOT NULL DEFAULT '',

    status       TEXT    NOT NULL DEFAULT 'draft'
                         CHECK (status IN ('draft', 'posted', 'voided')),

    created_at   TEXT    NOT NULL,

    -- Nullable id beside a required username, as in audit_log and
    -- ledger_entries: "console" is a truthful answer to who did something, and
    -- inventing a user id for it would be a claim this table cannot support.
    created_by            INTEGER REFERENCES users(id),
    created_by_username   TEXT    NOT NULL CHECK (length(trim(created_by_username)) > 0),

    posted_at             TEXT,
    posted_by             INTEGER REFERENCES users(id),
    posted_by_username    TEXT    NOT NULL DEFAULT '',

    voided_at             TEXT,
    voided_by             INTEGER REFERENCES users(id),
    voided_by_username    TEXT    NOT NULL DEFAULT '',

    -- Why it was voided, in the words of whoever voided it. The same sentence
    -- is copied onto every reversal the void writes, so a statement read years
    -- later carries the reason next to the numbers rather than in another table.
    void_reason           TEXT    NOT NULL DEFAULT '',

    -- Each status implies exactly what it implies. A draft that somehow holds a
    -- Ref. No., or a posted voucher without one, is a row no screen could
    -- render honestly.
    CHECK (status <> 'draft'  OR (ref_no IS NULL     AND posted_at IS NULL)),
    CHECK (status =  'draft'  OR (ref_no IS NOT NULL AND posted_at IS NOT NULL
                                  AND length(trim(posted_by_username)) > 0)),
    CHECK (status <> 'voided' OR (voided_at IS NOT NULL
                                  AND length(trim(voided_by_username)) > 0
                                  AND length(trim(void_reason)) > 0)),
    CHECK (status =  'voided' OR (voided_at IS NULL AND voided_by_username = '' AND void_reason = ''))
);

-- The Ref. No. sequence, unique across everything that has one. Partial, so
-- that the drafts carrying NULL are simply not in it.
--
-- This index is the backstop behind allocation inside the posting UPDATE. Step
-- 4 learned what a read-then-write allocator actually does under two encoders
-- working at once — 43 of 50 creates failing with SQLITE_BUSY rather than a
-- duplicate — and there is no reason to rediscover it here.
CREATE UNIQUE INDEX idx_advance_vouchers_ref_no ON advance_vouchers(ref_no)
    WHERE ref_no IS NOT NULL;

-- The list screen is "newest first", and every report in step 8 is a date range.
CREATE INDEX idx_advance_vouchers_date ON advance_vouchers(voucher_date);

-- One line of the grid: this farmer, this amount, this particular.
--
-- A line names its own customer. Whether one Ref. No. genuinely spans several
-- farmers is Q6 of the domain interview and is still unanswered; this is the
-- shape that is right either way, and it is what the legacy grid's Code and
-- Customer Name columns already say.
CREATE TABLE advance_lines (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,

    -- Cascade so that deleting a draft takes its lines with it. It can only
    -- ever fire for a draft: the trigger below refuses to delete a voucher that
    -- has left that status at all.
    voucher_id   INTEGER NOT NULL REFERENCES advance_vouchers(id) ON DELETE CASCADE,

    customer_id  INTEGER NOT NULL REFERENCES customers(id),

    -- The day this line's money moved, which is the date the ledger entry takes.
    -- It starts from the voucher's date and may differ from it: a voucher typed
    -- on Friday can carry a slip written on Wednesday.
    line_date    TEXT    NOT NULL CHECK (line_date LIKE '____-__-__'),

    particular   TEXT    NOT NULL DEFAULT '',

    -- Greater than zero. A negative advance is not an advance; money moving the
    -- other way is a payment or an adjustment, and both of those are documents
    -- of their own.
    amount_cents INTEGER NOT NULL CHECK (amount_cents > 0)
);

CREATE INDEX idx_advance_lines_voucher  ON advance_lines(voucher_id);

-- "What has this farmer been advanced?" — step 6's statement and step 8's
-- reports both walk this.
CREATE INDEX idx_advance_lines_customer ON advance_lines(customer_id);

-- What may change, and when.
--
-- Go enforces all of this too. These are here for the same reason the ledger's
-- triggers are: the realistic way a financial record gets edited is not through
-- the application at all, it is somebody opening bokkie.db in a SQLite browser
-- because they were sure about what the number should have been.

-- +goose StatementBegin
CREATE TRIGGER advance_vouchers_status_moves_one_way
BEFORE UPDATE OF status ON advance_vouchers
WHEN NEW.status <> OLD.status
BEGIN
    -- Two transitions exist: draft → posted and posted → voided. There is no
    -- un-post and no re-open. Correcting a posted voucher means voiding it and
    -- entering a new one, which leaves a legible trail of what actually
    -- happened rather than a row that has quietly been something else before.
    SELECT RAISE(ABORT, 'a voucher goes draft → posted → voided and no other way')
    WHERE NOT ((OLD.status = 'draft'  AND NEW.status = 'posted')
            OR (OLD.status = 'posted' AND NEW.status = 'voided'));

    -- Posting and voiding record what happened; they do not get to change what
    -- the voucher says. Naming the columns is what the ledger's triggers
    -- deliberately avoid, and it is unavoidable here only because this row
    -- legitimately changes twice in its life — so the list is kept to the
    -- fields a person would be shown.
    SELECT RAISE(ABORT, 'posting or voiding a voucher may not change what it says')
    WHERE NEW.voucher_date <> OLD.voucher_date
       OR NEW.notes        <> OLD.notes
       OR NEW.created_at   <> OLD.created_at
       OR NEW.created_by_username <> OLD.created_by_username;

    SELECT RAISE(ABORT, 'a posted voucher keeps the Ref. No. it was given')
    WHERE OLD.status = 'posted' AND NEW.ref_no IS NOT OLD.ref_no;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER advance_vouchers_are_frozen_once_posted
BEFORE UPDATE ON advance_vouchers
WHEN OLD.status <> 'draft' AND NEW.status = OLD.status
BEGIN
    SELECT RAISE(ABORT, 'a posted voucher cannot be edited: void it and enter a new one');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER advance_vouchers_are_deletable_only_as_drafts
BEFORE DELETE ON advance_vouchers
WHEN OLD.status <> 'draft'
BEGIN
    -- A draft moved no money, so deleting one loses nothing the ledger needed;
    -- what it said is kept in the audit row that records the deletion. A posted
    -- voucher is the opposite: it is the document behind entries that exist.
    SELECT RAISE(ABORT, 'a voucher that has been posted cannot be deleted: void it instead');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER advance_lines_belong_to_a_draft
BEFORE INSERT ON advance_lines
WHEN (SELECT status FROM advance_vouchers WHERE id = NEW.voucher_id) <> 'draft'
BEGIN
    SELECT RAISE(ABORT, 'a line cannot be added to a voucher that has been posted');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER advance_lines_are_frozen_once_posted_update
BEFORE UPDATE ON advance_lines
WHEN (SELECT status FROM advance_vouchers WHERE id = OLD.voucher_id) <> 'draft'
BEGIN
    SELECT RAISE(ABORT, 'the lines of a posted voucher cannot be edited: void it and enter a new one');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER advance_lines_are_frozen_once_posted_delete
BEFORE DELETE ON advance_lines
WHEN (SELECT status FROM advance_vouchers WHERE id = OLD.voucher_id) <> 'draft'
BEGIN
    -- This is also what makes the cascade above safe: when a draft is deleted
    -- the parent row is already gone, the subquery is NULL, and a NULL WHEN is
    -- not a match — so the cascade proceeds for a draft and for nothing else.
    SELECT RAISE(ABORT, 'the lines of a posted voucher cannot be deleted: void it and enter a new one');
END;
-- +goose StatementEnd

-- +goose Down

DROP TRIGGER advance_lines_are_frozen_once_posted_delete;
DROP TRIGGER advance_lines_are_frozen_once_posted_update;
DROP TRIGGER advance_lines_belong_to_a_draft;
DROP TRIGGER advance_vouchers_are_deletable_only_as_drafts;
DROP TRIGGER advance_vouchers_are_frozen_once_posted;
DROP TRIGGER advance_vouchers_status_moves_one_way;
DROP TABLE advance_lines;
DROP TABLE advance_vouchers;
