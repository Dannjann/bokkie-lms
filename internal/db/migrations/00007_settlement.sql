-- Step 7.5 — the settlement replaces the interest run.
--
-- The legacy program never charged interest across every farmer at once. It
-- settled one farmer at a time, when they delivered palay: interest on each
-- advance from its own date to a cut-off, at a monthly rate chosen on the day,
-- and the delivery applied interest first. See docs/plans/07.5-settlement.md
-- and docs/plans/legacy-findings.md for the evidence.
--
-- Three changes:
--
--   1. payment_lines — a palay delivery is recorded as the legacy screen
--      recorded it: class, sacks, kilos and price per kilo. Cost is computed
--      in Go and stored, so the line a farmer is shown is the line that was
--      summed.
--   2. settlements, with the advances and payments each one applied.
--   3. interest_runs goes. No release ever shipped with it, and a station-wide
--      run is not a thing the station does.

-- +goose Up

CREATE TABLE payment_lines (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,

    payment_id   INTEGER NOT NULL REFERENCES payments(id) ON DELETE CASCADE,

    -- The grade or variety code the station uses: SLG, YCS, DOLG and so on.
    -- Free text, because the list has grown by suffix (SLG CB, SLG BASA) for
    -- eighteen years and a closed list would refuse the next one.
    class        TEXT    NOT NULL DEFAULT '',

    sacks        INTEGER NOT NULL DEFAULT 0 CHECK (sacks >= 0),

    -- Kilos to one decimal place, held as tenths: 1234.5 kg is 12345. The
    -- legacy column was N(10,1).
    kilos_tenths INTEGER NOT NULL CHECK (kilos_tenths > 0),

    -- Price per kilo, in centavos.
    price_cents  INTEGER NOT NULL CHECK (price_cents > 0),

    -- kilos × price, rounded half-up to the centavo. Stored rather than
    -- recomputed so that what was printed is what was added up.
    cost_cents   INTEGER NOT NULL CHECK (cost_cents > 0)
);

CREATE INDEX idx_payment_lines_payment ON payment_lines(payment_id);

-- +goose StatementBegin
CREATE TRIGGER payment_lines_belong_to_a_draft
BEFORE INSERT ON payment_lines
WHEN (SELECT status FROM payments WHERE id = NEW.payment_id) <> 'draft'
BEGIN
    SELECT RAISE(ABORT, 'a line cannot be added to a payment that has been recorded');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER payment_lines_are_frozen_once_posted_update
BEFORE UPDATE ON payment_lines
WHEN (SELECT status FROM payments WHERE id = OLD.payment_id) <> 'draft'
BEGIN
    SELECT RAISE(ABORT, 'the lines of a recorded payment cannot be edited: void it and enter a new one');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER payment_lines_are_frozen_once_posted_delete
BEFORE DELETE ON payment_lines
WHEN (SELECT status FROM payments WHERE id = OLD.payment_id) <> 'draft'
BEGIN
    SELECT RAISE(ABORT, 'the lines of a recorded payment cannot be deleted: void it and enter a new one');
END;
-- +goose StatementEnd

CREATE TABLE settlements (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,

    -- NULL until posted, for the reason a Ref. No. is: a numbered draft that
    -- is then abandoned leaves a hole in a sequence people read as a run.
    settlement_no  INTEGER CHECK (settlement_no IS NULL OR settlement_no > 0),

    customer_id    INTEGER NOT NULL REFERENCES customers(id),

    -- Interest runs to this day, and nothing dated after it is included.
    cutoff_date    TEXT    NOT NULL CHECK (cutoff_date LIKE '____-__-__'),

    -- A monthly rate in basis points: 500 is 5% a month. Zero is allowed —
    -- the legacy data has two settlements at 0%, which is how a charge was
    -- waived.
    rate_bps       INTEGER NOT NULL CHECK (rate_bps >= 0),

    -- What the farmer owed before this settlement, as computed when it was
    -- drafted: the principal carried from before — negative when an earlier
    -- overpayment left them in credit, which is charged no interest — the
    -- unpaid interest carried from before, and since when the carried
    -- principal has been accruing. Stored because "why is this ₱150.00?" has
    -- to be answerable from the document after the balance has moved on.
    carried_principal_cents INTEGER NOT NULL DEFAULT 0,
    carried_interest_cents  INTEGER NOT NULL DEFAULT 0,
    carried_since           TEXT    CHECK (carried_since IS NULL OR carried_since LIKE '____-__-__'),
    carried_days            INTEGER NOT NULL DEFAULT 0 CHECK (carried_days >= 0),
    carried_charge_cents    INTEGER NOT NULL DEFAULT 0 CHECK (carried_charge_cents >= 0),

    notes          TEXT    NOT NULL DEFAULT '',

    status         TEXT    NOT NULL DEFAULT 'draft'
                           CHECK (status IN ('draft', 'posted', 'voided')),

    created_at            TEXT    NOT NULL,
    created_by            INTEGER REFERENCES users(id),
    created_by_username   TEXT    NOT NULL CHECK (length(trim(created_by_username)) > 0),

    posted_at             TEXT,
    posted_by             INTEGER REFERENCES users(id),
    posted_by_username    TEXT    NOT NULL DEFAULT '',

    voided_at             TEXT,
    voided_by             INTEGER REFERENCES users(id),
    voided_by_username    TEXT    NOT NULL DEFAULT '',
    void_reason           TEXT    NOT NULL DEFAULT '',

    CHECK (status <> 'draft'  OR (settlement_no IS NULL     AND posted_at IS NULL)),
    CHECK (status =  'draft'  OR (settlement_no IS NOT NULL AND posted_at IS NOT NULL
                                  AND length(trim(posted_by_username)) > 0)),
    CHECK (status <> 'voided' OR (voided_at IS NOT NULL
                                  AND length(trim(voided_by_username)) > 0
                                  AND length(trim(void_reason)) > 0)),
    CHECK (status =  'voided' OR (voided_at IS NULL AND voided_by_username = '' AND void_reason = ''))
);

CREATE UNIQUE INDEX idx_settlements_no ON settlements(settlement_no)
    WHERE settlement_no IS NOT NULL;
CREATE INDEX idx_settlements_customer ON settlements(customer_id, cutoff_date);
CREATE INDEX idx_settlements_cutoff ON settlements(cutoff_date);

-- One row per open advance the settlement charged interest on.
CREATE TABLE settlement_advances (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    settlement_id   INTEGER NOT NULL REFERENCES settlements(id) ON DELETE CASCADE,
    advance_line_id INTEGER NOT NULL REFERENCES advance_lines(id),
    line_date       TEXT    NOT NULL CHECK (line_date LIKE '____-__-__'),
    principal_cents INTEGER NOT NULL CHECK (principal_cents > 0),
    days            INTEGER NOT NULL CHECK (days >= 0),
    charge_cents    INTEGER NOT NULL CHECK (charge_cents >= 0),
    UNIQUE (settlement_id, advance_line_id)
);

CREATE INDEX idx_settlement_advances_settlement ON settlement_advances(settlement_id);
CREATE INDEX idx_settlement_advances_line ON settlement_advances(advance_line_id);

-- One row per recorded payment the settlement applied, and how it divided.
CREATE TABLE settlement_payments (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    settlement_id   INTEGER NOT NULL REFERENCES settlements(id) ON DELETE CASCADE,
    payment_id      INTEGER NOT NULL REFERENCES payments(id),
    principal_cents INTEGER NOT NULL CHECK (principal_cents >= 0),
    interest_cents  INTEGER NOT NULL CHECK (interest_cents >= 0),
    CHECK (principal_cents + interest_cents > 0),
    UNIQUE (settlement_id, payment_id)
);

CREATE INDEX idx_settlement_payments_settlement ON settlement_payments(settlement_id);
CREATE INDEX idx_settlement_payments_payment ON settlement_payments(payment_id);

-- +goose StatementBegin
CREATE TRIGGER settlements_status_moves_one_way
BEFORE UPDATE OF status ON settlements
WHEN NEW.status <> OLD.status
BEGIN
    SELECT RAISE(ABORT, 'a settlement goes draft → posted → voided and no other way')
    WHERE NOT ((OLD.status = 'draft'  AND NEW.status = 'posted')
            OR (OLD.status = 'posted' AND NEW.status = 'voided'));
    SELECT RAISE(ABORT, 'posting or voiding a settlement may not change what it computed')
    WHERE NEW.customer_id <> OLD.customer_id
       OR NEW.cutoff_date <> OLD.cutoff_date
       OR NEW.rate_bps    <> OLD.rate_bps
       OR NEW.carried_principal_cents <> OLD.carried_principal_cents
       OR NEW.carried_interest_cents  <> OLD.carried_interest_cents
       OR NEW.carried_charge_cents    <> OLD.carried_charge_cents
       OR NEW.notes       <> OLD.notes
       OR NEW.created_at  <> OLD.created_at
       OR NEW.created_by_username <> OLD.created_by_username;
    SELECT RAISE(ABORT, 'a posted settlement keeps the number it was given')
    WHERE OLD.status = 'posted' AND NEW.settlement_no IS NOT OLD.settlement_no;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER settlements_are_frozen_once_posted
BEFORE UPDATE ON settlements
WHEN OLD.status <> 'draft' AND NEW.status = OLD.status
BEGIN
    SELECT RAISE(ABORT, 'a posted settlement cannot be edited: void it and settle again');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER settlements_are_deletable_only_as_drafts
BEFORE DELETE ON settlements
WHEN OLD.status <> 'draft'
BEGIN
    SELECT RAISE(ABORT, 'a settlement that has been posted cannot be deleted: void it instead');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER settlement_advances_belong_to_a_draft
BEFORE INSERT ON settlement_advances
WHEN (SELECT status FROM settlements WHERE id = NEW.settlement_id) <> 'draft'
BEGIN
    SELECT RAISE(ABORT, 'a line cannot be added to a settlement that has been posted');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER settlement_advances_are_frozen_update
BEFORE UPDATE ON settlement_advances
WHEN (SELECT status FROM settlements WHERE id = OLD.settlement_id) <> 'draft'
BEGIN
    SELECT RAISE(ABORT, 'the lines of a posted settlement cannot be edited');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER settlement_advances_are_frozen_delete
BEFORE DELETE ON settlement_advances
WHEN (SELECT status FROM settlements WHERE id = OLD.settlement_id) <> 'draft'
BEGIN
    SELECT RAISE(ABORT, 'the lines of a posted settlement cannot be deleted');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER settlement_payments_belong_to_a_draft
BEFORE INSERT ON settlement_payments
WHEN (SELECT status FROM settlements WHERE id = NEW.settlement_id) <> 'draft'
BEGIN
    SELECT RAISE(ABORT, 'a payment cannot be added to a settlement that has been posted');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER settlement_payments_are_frozen_update
BEFORE UPDATE ON settlement_payments
WHEN (SELECT status FROM settlements WHERE id = OLD.settlement_id) <> 'draft'
BEGIN
    SELECT RAISE(ABORT, 'the payments of a posted settlement cannot be edited');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER settlement_payments_are_frozen_delete
BEFORE DELETE ON settlement_payments
WHEN (SELECT status FROM settlements WHERE id = OLD.settlement_id) <> 'draft'
BEGIN
    SELECT RAISE(ABORT, 'the payments of a posted settlement cannot be deleted');
END;
-- +goose StatementEnd

DROP TRIGGER interest_run_lines_are_frozen_once_posted_delete;
DROP TRIGGER interest_run_lines_are_frozen_once_posted_update;
DROP TRIGGER interest_run_lines_belong_to_a_draft;
DROP TRIGGER interest_runs_are_deletable_only_as_drafts;
DROP TRIGGER interest_runs_are_frozen_once_posted;
DROP TRIGGER interest_runs_status_moves_one_way;
DROP TABLE interest_run_lines;
DROP TABLE interest_runs;

-- +goose Down

DROP TRIGGER settlement_payments_are_frozen_delete;
DROP TRIGGER settlement_payments_are_frozen_update;
DROP TRIGGER settlement_payments_belong_to_a_draft;
DROP TRIGGER settlement_advances_are_frozen_delete;
DROP TRIGGER settlement_advances_are_frozen_update;
DROP TRIGGER settlement_advances_belong_to_a_draft;
DROP TRIGGER settlements_are_deletable_only_as_drafts;
DROP TRIGGER settlements_are_frozen_once_posted;
DROP TRIGGER settlements_status_moves_one_way;
DROP TABLE settlement_payments;
DROP TABLE settlement_advances;
DROP TABLE settlements;
DROP TRIGGER payment_lines_are_frozen_once_posted_delete;
DROP TRIGGER payment_lines_are_frozen_once_posted_update;
DROP TRIGGER payment_lines_belong_to_a_draft;
DROP TABLE payment_lines;

-- The interest run, exactly as 00006 created it.

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

