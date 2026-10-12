-- +goose Up
UPDATE user_publications up
SET analysis_requested_at=now()
FROM users u
WHERE u.id=up.user_id AND u.active AND u.llm_consent
  AND up.analysis_requested_at IS NULL;

-- +goose Down
-- Analysis requests are deliberately retained: reverting this migration must
-- not erase work already queued or completed for a lawyer.
