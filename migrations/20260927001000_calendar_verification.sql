-- +goose Up
ALTER TABLE court_calendar_versions ADD COLUMN coverage_verified BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose Down
ALTER TABLE court_calendar_versions DROP COLUMN coverage_verified;
