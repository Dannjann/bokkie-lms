-- Baseline. Step 1 deliberately creates no domain tables: this migration exists
-- so the migration machinery itself — goose's version table, the embedded FS,
-- and the refuse-to-start check for a newer schema — is exercised and proven
-- before the first table that holds money is written in step 3.

-- +goose Up
SELECT 1;

-- +goose Down
SELECT 1;
