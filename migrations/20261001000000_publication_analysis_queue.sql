-- +goose Up
ALTER TABLE user_publications
    ADD COLUMN analysis_requested_at TIMESTAMPTZ,
    ADD COLUMN analysis_started_at TIMESTAMPTZ,
    ADD COLUMN analysis_next_attempt_at TIMESTAMPTZ;

-- Preserve analyses already performed by earlier versions. Unread publications
-- that were never analyzed remain outside the queue until a lawyer opens them.
UPDATE user_publications
SET analysis_requested_at=created_at
WHERE is_read OR analysis_status<>'PENDING';

UPDATE user_publications
SET analysis_status='RETRY', analysis_started_at=NULL, analysis_next_attempt_at=now()
WHERE analysis_status='RUNNING';

CREATE INDEX user_publications_analysis_queue_idx
    ON user_publications(analysis_next_attempt_at, created_at)
    WHERE analysis_requested_at IS NOT NULL AND analysis_status IN ('PENDING','RETRY','RUNNING');
