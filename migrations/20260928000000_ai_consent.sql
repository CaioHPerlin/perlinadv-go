-- +goose Up
ALTER TABLE users ADD COLUMN llm_consented_at TIMESTAMPTZ;
UPDATE users SET llm_consented_at=now() WHERE llm_consent=TRUE;
ALTER TABLE user_publications ADD COLUMN llm_assisted BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE user_publications DROP COLUMN llm_assisted;
ALTER TABLE users DROP COLUMN llm_consented_at;
