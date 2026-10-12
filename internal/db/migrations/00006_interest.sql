-- Step 7 — the interest run.
--
-- The first document in the system that charges money without anybody lending
-- any. Every entry until now came from a slip somebody typed; a run is computed
-- from the record itself, against every borrower at once, which makes it the
-- most dangerous act in the application. An advance posted wrong is one
-- farmer's voucher and one conversation. A run posted wrong is three hundred
-- and sixty-four balances, all slightly wrong, all plausible.
--
-- So the shape here is the voucher's — a header with lines beneath it, drafted,
-- posted once, and undone only by reversing every line at once — and the extra
-- rules are all about the two ways a run goes wrong at scale: charging a period
-- twice, and charging one farmer twice inside one run. Both are refused by this
-- schema and not only by Go.
--
-- The rule that produced a run is copied onto it rather than read back from
-- settings. A run opened in two years has to be able to say what rate made it,
-- and by then the setting will have moved.

-- +goose Up

CREATE TABLE interest_runs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,

    -- NULL until the run is posted, and allocated inside the posting UPDATE —
    -- the ref_no and or_no arrangement exactly. Unlike those two there is no
    -- *.start setting behind it: Ref. No. and OR No. each continue a legacy
    -- sequence staff read off paper, and there is no legacy run sequence to
    -- continue.
    run_no       INTEGER CHECK (run_no IS NULL OR run_no > 0),

    -- The period charged for. A pair rather than a single "as of" day because
    -- both methods need both ends: charging on a balance reads it at period_to,
    -- and charging on what was advanced needs the range to know which advances
    -- it covers. It also means Q1 answering "per cropping season" is a
    -- different pair of dates rather than a different table.
    period_from  TEXT    NOT NULL CHECK (period_from LIKE '____-__-__'),
    period_to    TEXT    NOT NULL CHECK (period_to   LIKE '____-__-__'),

    -- The rule, as it stood when this run was computed. 'none' is absent from
    -- the list on purpose: it is a legal setting and it means no run exists at
    -- all, so a row holding it would be a run that charges nothing.
    method       TEXT    NOT NULL CHECK (method IN ('monthly-on-balance', 'flat-on-advance')),

    -- Basis points. 300 is 3%. Never a decimal: a rate stored as "3.5" is a
    -- float waiting to happen, and the money package has a test that fails the
    -- build over one.
    rate_bps     INTEGER NOT NULL CHECK (rate_bps > 0),

    basis        TEXT    NOT NULL CHECK (basis IN ('principal', 'principal-and-interest')),

    notes        TEXT    NOT NULL DEFAULT '',

    status       TEXT    NOT NULL DEFAULT 'draft'
                         CHECK (status IN ('draft', 'posted', 'voided')),

    created_at            TEXT    NOT NULL,

    -- Nullable id beside a required username, as everywhere else that records
    -- who did something: "console" is a truthful answer to who, and inventing a
    -- user id for it would be a claim this table cannot support.
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

    -- A period runs forwards. SQLite compares these as text, which is what
    -- makes storing dates as ISO strings worth the four wasted bytes.
    CHECK (period_to >= period_from),

    -- Each status implies exactly what it implies.
    CHECK (status <> 'draft'  OR (run_no IS NULL     AND posted_at IS NULL)),
    CHECK (status =  'draft'  OR (run_no IS NOT NULL AND posted_at IS NOT NULL
                                  AND length(trim(posted_by_username)) > 0)),
    CHECK (status <> 'voided' OR (voided_at IS NOT NULL
                                  AND length(trim(voided_by_username)) > 0
                                  AND length(trim(void_reason)) > 0)),
    CHECK (status =  'voided' OR (voided_at IS NULL AND voided_by_username = '' AND void_reason = ''))
);

-- The run sequence. Partial, so the drafts carrying NULL are simply not in it.
CREATE UNIQUE INDEX idx_interest_runs_run_no ON interest_runs(run_no)
    WHERE run_no IS NOT NULL;

-- **The same period cannot be charged twice.**
--
-- This index is half of that guard and it is the half that catches the ordinary
-- case: two drafts of September, and somebody clicking Post on both. The other
-- half is an overlap check in Go inside the posting transaction, because an
-- index cannot express that 1–30 September and 15 September–15 October
-- intersect. Neither half is redundant.
--
-- Partial on 'posted', so a voided run frees its period. That is right: a run
-- voided because the rate was wrong has to be re-runnable.
CREATE UNIQUE INDEX idx_interest_runs_period ON interest_runs(period_from, period_to)
    WHERE status = 'posted';

-- The list screen is "newest first", and step 8's reports are date ranges.
CREATE INDEX idx_interest_runs_period_to ON interest_runs(period_to);

-- One line: this farmer, what the charge was computed from, and the charge.
--
-- A line exists only where money is actually charged. A farmer who owes
-- nothing, or who is in credit, produces no row — a preview padded with three
-- hundred zeroes hides the twelve lines that matter, and an entry that moves no
-- money is refused by the ledger anyway.
CREATE TABLE interest_run_lines (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,

    -- Cascade so that deleting a draft takes its lines with it. It can only
    -- ever fire for a draft: the trigger below refuses to delete a run that has
    -- left that status at all.
    run_id       INTEGER NOT NULL REFERENCES interest_runs(id) ON DELETE CASCADE,

    customer_id  INTEGER NOT NULL REFERENCES customers(id),

    -- What the charge was computed from — the balance at the end of the period,
    -- or what was advanced during it, depending on the run's method. Stored
    -- because "why is this ₱150.00?" is the question a farmer asks, and
    -- recomputing the answer from a balance that has since moved does not
    -- answer it.
    base_cents   INTEGER NOT NULL CHECK (base_cents > 0),

    charge_cents INTEGER NOT NULL CHECK (charge_cents > 0),

    -- One row per farmer per run. The schema says so rather than the code
    -- remembering to.
    UNIQUE (run_id, customer_id)
);

CREATE INDEX idx_interest_run_lines_run ON interest_run_lines(run_id);

-- "What interest has this farmer been charged?" — the statement's labels and
-- step 8's reports both walk this.
CREATE INDEX idx_interest_run_lines_customer ON interest_run_lines(customer_id);

-- What may change, and when.
--
-- Go enforces all of this too. These are here for the reason the ledger's, the
-- voucher's and the payment's triggers are: the realistic way a financial
-- record gets edited is not through the application at all, it is somebody
-- opening bokkie.db in a SQLite browser because they were sure about what the
-- number should have been.

-- +goose StatementBegin
CREATE TRIGGER interest_runs_status_moves_one_way
BEFORE UPDATE OF status ON interest_runs
WHEN NEW.status <> OLD.status
BEGIN
    SELECT RAISE(ABORT, 'an interest run goes draft → posted → voided and no other way')
    WHERE NOT ((OLD.status = 'draft'  AND NEW.status = 'posted')
            OR (OLD.status = 'posted' AND NEW.status = 'voided'));

    -- Posting and voiding record what happened; they do not get to change what
    -- the run charged or what rule it charged it under. The rate especially:
    -- a run whose rate could be edited after posting is a document that cannot
    -- be used to explain the entries beneath it.
    SELECT RAISE(ABORT, 'posting or voiding a run may not change what it charged')
    WHERE NEW.period_from <> OLD.period_from
       OR NEW.period_to   <> OLD.period_to
       OR NEW.method      <> OLD.method
       OR NEW.rate_bps    <> OLD.rate_bps
       OR NEW.basis       <> OLD.basis
       OR NEW.notes       <> OLD.notes
       OR NEW.created_at  <> OLD.created_at
       OR NEW.created_by_username <> OLD.created_by_username;

    SELECT RAISE(ABORT, 'a posted run keeps the number it was given')
    WHERE OLD.status = 'posted' AND NEW.run_no IS NOT OLD.run_no;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER interest_runs_are_frozen_once_posted
BEFORE UPDATE ON interest_runs
WHEN OLD.status <> 'draft' AND NEW.status = OLD.status
BEGIN
    SELECT RAISE(ABORT, 'a posted interest run cannot be edited: void it and run it again');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER interest_runs_are_deletable_only_as_drafts
BEFORE DELETE ON interest_runs
WHEN OLD.status <> 'draft'
BEGIN
    -- A draft charged nothing, so deleting one loses nothing the ledger needed;
    -- what it said is kept in the audit row that records the deletion. A posted
    -- run is the opposite: it is the document behind entries that exist.
    SELECT RAISE(ABORT, 'an interest run that has been posted cannot be deleted: void it instead');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER interest_run_lines_belong_to_a_draft
BEFORE INSERT ON interest_run_lines
WHEN (SELECT status FROM interest_runs WHERE id = NEW.run_id) <> 'draft'
BEGIN
    SELECT RAISE(ABORT, 'a line cannot be added to an interest run that has been posted');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER interest_run_lines_are_frozen_once_posted_update
BEFORE UPDATE ON interest_run_lines
WHEN (SELECT status FROM interest_runs WHERE id = OLD.run_id) <> 'draft'
BEGIN
    SELECT RAISE(ABORT, 'the lines of a posted interest run cannot be edited: void it and run it again');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER interest_run_lines_are_frozen_once_posted_delete
BEFORE DELETE ON interest_run_lines
WHEN (SELECT status FROM interest_runs WHERE id = OLD.run_id) <> 'draft'
BEGIN
    -- This is also what makes the cascade above safe: when a draft is deleted
    -- the parent row is already gone, the subquery is NULL, and a NULL WHEN is
    -- not a match — so the cascade proceeds for a draft and for nothing else.
    SELECT RAISE(ABORT, 'the lines of a posted interest run cannot be deleted: void it and run it again');
END;
-- +goose StatementEnd

-- +goose Down

DROP TRIGGER interest_run_lines_are_frozen_once_posted_delete;
DROP TRIGGER interest_run_lines_are_frozen_once_posted_update;
DROP TRIGGER interest_run_lines_belong_to_a_draft;
DROP TRIGGER interest_runs_are_deletable_only_as_drafts;
DROP TRIGGER interest_runs_are_frozen_once_posted;
DROP TRIGGER interest_runs_status_moves_one_way;
DROP TABLE interest_run_lines;
DROP TABLE interest_runs;
