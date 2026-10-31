-- Editing a posted advance slip, keeping its Ref. No.
--
-- The client wants posted slips editable where they are read: on the slip, in
-- a customer's transactions, in a liquidation. The ledger stays append-only,
-- so an edit is a correction made in one transaction: the original slip is
-- voided — its entries reversed — and a replacement is posted carrying the
-- edited lines. The client's rule is that the number on the paper slip does
-- not change, so the replacement takes the same Ref. No. at the next revision.
--
--   Ref. No. 1425 rev 1  voided, superseded_by → the row below
--   Ref. No. 1425 rev 2  posted, the slip as it now reads
--
-- Nothing here loosens a trigger: the original goes posted → voided as any
-- void does, and the replacement goes draft → posted as any post does.

-- +goose Up

ALTER TABLE advance_vouchers ADD COLUMN revision INTEGER NOT NULL DEFAULT 1 CHECK (revision >= 1);

-- Set in the same UPDATE that voids the original, because a voided row is
-- frozen from then on. Only a voided slip can have been superseded.
ALTER TABLE advance_vouchers ADD COLUMN superseded_by INTEGER REFERENCES advance_vouchers(id)
    CHECK (superseded_by IS NULL OR status = 'voided');

-- The Ref. No. is now unique per revision rather than outright. The partial
-- index is still the backstop behind allocation: a fresh number is MAX + 1,
-- and it lands at revision 1.
DROP INDEX idx_advance_vouchers_ref_no;
CREATE UNIQUE INDEX idx_advance_vouchers_ref_no ON advance_vouchers(ref_no, revision)
    WHERE ref_no IS NOT NULL;

-- +goose Down

DROP INDEX idx_advance_vouchers_ref_no;
CREATE UNIQUE INDEX idx_advance_vouchers_ref_no ON advance_vouchers(ref_no)
    WHERE ref_no IS NOT NULL;
ALTER TABLE advance_vouchers DROP COLUMN superseded_by;
ALTER TABLE advance_vouchers DROP COLUMN revision;
