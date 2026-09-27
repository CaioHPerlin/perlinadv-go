-- +goose Up
CREATE TABLE IF NOT EXISTS djen_lawyer_syncs (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tribunal TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','DONE','FAILED')),
    last_started_at TIMESTAMPTZ,
    last_succeeded_at TIMESTAMPTZ,
    item_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (user_id, tribunal)
);

-- +goose Down
DROP TABLE djen_lawyer_syncs;
