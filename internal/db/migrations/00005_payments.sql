-- Step 6 — the payment receipt.
--
-- The document that moves money back. Where an advance voucher is a header with
-- a grid of lines beneath it, a payment is a header and nothing else: one
-- farmer at the counter handing money over and being given a receipt. A receipt
-- covering three payers is not a thing that happens, so there is no line table
-- here and the customer sits on the receipt itself.
--
-- The rules are the voucher's rules, and for the same reason: a draft is meant
-- to be edited, and the moment it is posted it describes money that has moved.
-- The triggers below key off OLD.status rather than off a list of columns, so
-- they keep covering any column a later migration adds.
--
-- What this table does *not* hold is the split between principal and interest.
-- That lives on the ledger entry the receipt produces, which already carries
-- both deltas and already refuses a positive one. A copy of it here would be a
-- number that can disagree with the record.

-- +goose Up

CREATE TABLE payments (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,

    -- NULL until the payment is posted. Numbering a draft would let an
    -- abandoned one leave a permanent hole in a sequence staff read as a run,
    -- and at cutover this sequence continues the legacy one, where a gap is a
    -- question somebody will ask. Separate from the voucher's Ref. No.: they
    -- are separate pads of paper.
    or_no          INTEGER CHECK (or_no IS NULL OR or_no > 0),

    -- No cascade and no ON DELETE: a customer is never deleted in this system,
    -- which is what makes a receipt's customer a permanent reference.
    customer_id    INTEGER NOT NULL REFERENCES customers(id),

    -- The business date — the day the money came in, in station-local time, in
    -- the same shape as a ledger entry's. It is the date the entry takes.
    payment_date   TEXT    NOT NULL CHECK (payment_date LIKE '____-__-__'),

    -- Greater than zero. A negative payment is not a payment; money moving the
    -- other way is an advance or an adjustment, and both are documents of their
    -- own.
    amount_cents   INTEGER NOT NULL CHECK (amount_cents > 0),

    -- How the money arrived. Q2 of the domain interview — whether repayment is
    -- cash or palay netted against the advance — is unanswered, and this column
    -- is what makes it answerable from real data rather than from memory. It
    -- records the method and not the mechanics: no kilos and no price per kilo,
    -- because if the answer is "we compute the delivery value here" then what
    -- is needed is a delivery document that produces a peso figure and feeds a
    -- payment, not two more columns on this table.
    method         TEXT    NOT NULL DEFAULT 'cash'
                           CHECK (method IN ('cash', 'palay', 'other')),

    particular     TEXT    NOT NULL DEFAULT '',

    status         TEXT    NOT NULL DEFAULT 'draft'
                           CHECK (status IN ('draft', 'posted', 'voided')),

    created_at            TEXT    NOT NULL,

    -- Nullable id beside a required username, as in audit_log, ledger_entries
    -- and advance_vouchers: "console" is a truthful answer to who did
    -- something, and inventing a user id for it would be a claim this table
    -- cannot support.
    created_by            INTEGER REFERENCES users(id),
    created_by_username   TEXT    NOT NULL CHECK (length(trim(created_by_username)) > 0),

    posted_at             TEXT,
    posted_by             INTEGER REFERENCES users(id),
    posted_by_username    TEXT    NOT NULL DEFAULT '',

    voided_at             TEXT,
    voided_by             INTEGER REFERENCES users(id),
    voided_by_username    TEXT    NOT NULL DEFAULT '',

    -- Why it was voided, in the words of whoever voided it. The same sentence
    -- is copied onto the reversal, so a statement read years later carries the
    -- reason next to the numbers rather than in another table.
    void_reason           TEXT    NOT NULL DEFAULT '',

    -- Each status implies exactly what it implies. A draft holding an OR No.,
    -- or a posted receipt without one, is a row no screen could render
    -- honestly.
    CHECK (status <> 'draft'  OR (or_no IS NULL     AND posted_at IS NULL)),
    CHECK (status =  'draft'  OR (or_no IS NOT NULL AND posted_at IS NOT NULL
                                  AND length(trim(posted_by_username)) > 0)),
    CHECK (status <> 'voided' OR (voided_at IS NOT NULL
                                  AND length(trim(voided_by_username)) > 0
                                  AND length(trim(void_reason)) > 0)),
    CHECK (status =  'voided' OR (voided_at IS NULL AND voided_by_username = '' AND void_reason = ''))
);

-- The OR No. sequence, unique across everything that has one. Partial, so the
-- drafts carrying NULL are simply not in it.
--
-- This index is the backstop behind allocation inside the posting UPDATE, the
-- same arrangement as the voucher's Ref. No. Step 4 learned what a
-- read-then-write allocator does under two encoders working at once — 43 of 50
-- creates failing with SQLITE_BUSY rather than a duplicate — and step 5 proved
-- the fix; there is no reason to rediscover either here.
CREATE UNIQUE INDEX idx_payments_or_no ON payments(or_no) WHERE or_no IS NOT NULL;

-- The list screen is "newest first", and every report in step 8 is a date range.
CREATE INDEX idx_payments_date ON payments(payment_date);

-- "What has this farmer paid?" — the statement and step 8's reports walk this.
CREATE INDEX idx_payments_customer ON payments(customer_id);

-- What may change, and when.
--
-- Go enforces all of this too. These are here for the reason the ledger's and
-- the voucher's triggers are: the realistic way a financial record gets edited
-- is not through the application at all, it is somebody opening bokkie.db in a
-- SQLite browser because they were sure about what the number should have been.

-- +goose StatementBegin
CREATE TRIGGER payments_status_moves_one_way
BEFORE UPDATE OF status ON payments
WHEN NEW.status <> OLD.status
BEGIN
    -- Two transitions exist: draft → posted and posted → voided. There is no
    -- un-post and no re-open. Correcting a posted payment means voiding it and
    -- entering a new one, which leaves a legible trail of what actually
    -- happened rather than a row that has quietly been something else before.
    SELECT RAISE(ABORT, 'a payment goes draft → posted → voided and no other way')
    WHERE NOT ((OLD.status = 'draft'  AND NEW.status = 'posted')
            OR (OLD.status = 'posted' AND NEW.status = 'voided'));

    -- Posting and voiding record what happened; they do not get to change what
    -- the receipt says. Naming the columns is what the ledger's triggers
    -- deliberately avoid, and it is unavoidable here only because this row
    -- legitimately changes twice in its life — so the list is kept to the
    -- fields a person would be shown.
    SELECT RAISE(ABORT, 'posting or voiding a payment may not change what it says')
    WHERE NEW.customer_id  <> OLD.customer_id
       OR NEW.payment_date <> OLD.payment_date
       OR NEW.amount_cents <> OLD.amount_cents
       OR NEW.method       <> OLD.method
       OR NEW.particular   <> OLD.particular
       OR NEW.created_at   <> OLD.created_at
       OR NEW.created_by_username <> OLD.created_by_username;

    SELECT RAISE(ABORT, 'a posted payment keeps the OR No. it was given')
    WHERE OLD.status = 'posted' AND NEW.or_no IS NOT OLD.or_no;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER payments_are_frozen_once_posted
BEFORE UPDATE ON payments
WHEN OLD.status <> 'draft' AND NEW.status = OLD.status
BEGIN
    SELECT RAISE(ABORT, 'a posted payment cannot be edited: void it and enter a new one');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER payments_are_deletable_only_as_drafts
BEFORE DELETE ON payments
WHEN OLD.status <> 'draft'
BEGIN
    -- A draft moved no money, so deleting one loses nothing the ledger needed;
    -- what it said is kept in the audit row that records the deletion. A posted
    -- payment is the opposite: it is the document behind an entry that exists.
    SELECT RAISE(ABORT, 'a payment that has been posted cannot be deleted: void it instead');
END;
-- +goose StatementEnd

-- +goose Down

DROP TRIGGER payments_are_deletable_only_as_drafts;
DROP TRIGGER payments_are_frozen_once_posted;
DROP TRIGGER payments_status_moves_one_way;
DROP TABLE payments;
