-- +goose Up
CREATE TABLE lawyer_onboarding_syncs (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'RUNNING', 'DONE', 'FAILED')),
    progress SMALLINT NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    current_tribunal TEXT NOT NULL DEFAULT '',
    publication_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    acknowledged_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX lawyer_onboarding_syncs_pending_idx ON lawyer_onboarding_syncs(updated_at)
    WHERE status IN ('PENDING', 'RUNNING');

-- +goose Down
DROP TABLE lawyer_onboarding_syncs;
