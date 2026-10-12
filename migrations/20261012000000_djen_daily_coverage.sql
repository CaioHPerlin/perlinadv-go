-- +goose Up
ALTER TABLE djen_lawyer_syncs ADD COLUMN last_covered_on DATE;
ALTER TABLE djen_lawyer_syncs ADD COLUMN retry_from_on DATE;
UPDATE djen_lawyer_syncs SET last_covered_on=(last_succeeded_at AT TIME ZONE 'America/Campo_Grande')::date
WHERE last_succeeded_at IS NOT NULL;

-- +goose Down
ALTER TABLE djen_lawyer_syncs DROP COLUMN last_covered_on;
ALTER TABLE djen_lawyer_syncs DROP COLUMN retry_from_on;
