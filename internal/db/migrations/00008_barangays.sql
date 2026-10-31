-- Barangays, and customer codes that say which one a farmer is from.
--
-- The client's rule: a customer's code is assigned per barangay — a farmer
-- from Pintor is PI-0001, one from Camarag is CMG-0001 — each with its own
-- sequence. The manager keeps the list of barangays and chooses each prefix.
--
-- Customers already on the books keep their BA-#### codes. A code is what is
-- written on the farmer's paper slips, so re-coding anybody would orphan every
-- slip already handed out; their barangay starts empty and can be set later
-- without touching the code.

-- +goose Up

CREATE TABLE barangays (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL COLLATE NOCASE UNIQUE
                       CHECK (length(trim(name)) > 0),

    -- Two to four capital letters. It becomes the front of every code handed
    -- out in this barangay, so it is fixed once a customer holds one: Go
    -- refuses the change, and the codes already issued are the reason why.
    prefix     TEXT    NOT NULL UNIQUE
                       CHECK (prefix GLOB '[A-Z][A-Z]'
                           OR prefix GLOB '[A-Z][A-Z][A-Z]'
                           OR prefix GLOB '[A-Z][A-Z][A-Z][A-Z]'),

    -- A barangay is retired, never deleted, for the same reason a customer is:
    -- its customers still point at it.
    active     INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),

    created_at TEXT    NOT NULL,
    updated_at TEXT    NOT NULL
);

-- Nullable: the customers who predate barangays have none until somebody sets
-- one, and the import does not know them either.
ALTER TABLE customers ADD COLUMN barangay_id INTEGER REFERENCES barangays(id);

CREATE INDEX idx_customers_barangay ON customers(barangay_id);

-- +goose Down

DROP INDEX idx_customers_barangay;
ALTER TABLE customers DROP COLUMN barangay_id;
DROP TABLE barangays;
