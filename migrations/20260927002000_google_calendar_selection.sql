-- +goose Up
ALTER TABLE google_connections ALTER COLUMN calendar_id SET DEFAULT '';
UPDATE google_connections gc SET calendar_id=''
WHERE gc.calendar_id='primary' AND NOT EXISTS (
    SELECT 1 FROM google_events ge JOIN deadlines d ON d.id=ge.deadline_id
    WHERE d.user_id=gc.user_id
);

-- +goose Down
ALTER TABLE google_connections ALTER COLUMN calendar_id SET DEFAULT 'primary';
