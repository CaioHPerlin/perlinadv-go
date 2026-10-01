package worker

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/caiohperlin/perlinadv/internal/google"
)

type googleJob struct {
	id, user, deadline, operation, eventID, calendarID string
	attempts                                           int
}

func (w Worker) SyncGoogle(ctx context.Context) error {
	c, err := google.New()
	if err != nil || !c.Configured() {
		return nil
	}
	conn, err := w.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var locked bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(7242027)`).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(7242027)`)
	rows, err := w.DB.QueryContext(ctx, `SELECT id::text,user_id::text,coalesce(deadline_id::text,''),operation,event_id,calendar_id,attempts FROM google_jobs WHERE status='PENDING' AND next_attempt_at<=now() ORDER BY created_at LIMIT 100`)
	if err != nil {
		return err
	}
	jobs := []googleJob{}
	for rows.Next() {
		var j googleJob
		if rows.Scan(&j.id, &j.user, &j.deadline, &j.operation, &j.eventID, &j.calendarID, &j.attempts) == nil {
			jobs = append(jobs, j)
		}
	}
	rows.Close()
	for _, j := range jobs {
		err = w.processGoogle(ctx, c, j)
		if err == nil {
			_, _ = w.DB.ExecContext(ctx, `UPDATE google_jobs SET status='DONE',last_error='' WHERE id=$1`, j.id)
		} else {
			delay := 1 << min(j.attempts, 7)
			_, _ = w.DB.ExecContext(ctx, `UPDATE google_jobs SET attempts=attempts+1,last_error=$2,status=CASE WHEN attempts>=7 THEN 'FAILED' ELSE 'PENDING' END,next_attempt_at=now()+($3::text||' minutes')::interval WHERE id=$1`, j.id, err.Error(), delay)
		}
	}
	return nil
}
func (w Worker) processGoogle(ctx context.Context, c google.Client, j googleJob) error {
	var cipher []byte
	var selected string
	err := w.DB.QueryRowContext(ctx, `SELECT refresh_token_cipher,calendar_id FROM google_connections WHERE user_id=$1 AND disabled_at IS NULL`, j.user).Scan(&cipher, &selected)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if selected == "" && j.operation != "DELETE" {
		return fmt.Errorf("Google calendar not selected")
	}
	refresh, err := c.Decrypt(cipher)
	if err != nil {
		return err
	}
	tok, err := c.Refresh(ctx, refresh)
	if err != nil {
		return err
	}
	if j.operation == "DELETE" {
		if j.eventID == "" {
			return nil
		}
		status, _, err := c.Call(ctx, tok.AccessToken, "DELETE", "/calendars/"+url.PathEscape(j.calendarID)+"/events/"+url.PathEscape(j.eventID), nil)
		if err != nil {
			return err
		}
		if status != 204 && status != 404 && status != 410 {
			return fmt.Errorf("Google delete HTTP %d", status)
		}
		_, _ = w.DB.ExecContext(ctx, `DELETE FROM google_events WHERE event_id=$1 AND calendar_id=$2`, j.eventID, j.calendarID)
		return nil
	}
	if j.deadline == "" {
		return nil
	}
	var title, desc, due, status string
	var needsReview bool
	err = w.DB.QueryRowContext(ctx, `SELECT title,description,due_on::text,status,needs_review FROM deadlines WHERE id=$1 AND user_id=$2`, j.deadline, j.user).Scan(&title, &desc, &due, &status, &needsReview)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if status != "ACTIVE" || needsReview {
		return nil
	}
	date, err := time.Parse("2006-01-02", due)
	if err != nil {
		return err
	}
	eventID := deterministicEventID(j.deadline)
	path := "/calendars/" + url.PathEscape(selected) + "/events"
	event := map[string]any{"id": eventID, "summary": "Prazo: " + title, "description": desc, "start": map[string]string{"date": due}, "end": map[string]string{"date": date.AddDate(0, 0, 1).Format("2006-01-02")}, "extendedProperties": map[string]any{"private": map[string]string{"perlinadvDeadlineId": j.deadline}}}
	var oldCalendar, oldID string
	_ = w.DB.QueryRowContext(ctx, `SELECT calendar_id,event_id FROM google_events WHERE deadline_id=$1`, j.deadline).Scan(&oldCalendar, &oldID)
	if oldID != "" && oldCalendar != selected {
		code, _, err := c.Call(ctx, tok.AccessToken, "DELETE", "/calendars/"+url.PathEscape(oldCalendar)+"/events/"+url.PathEscape(oldID), nil)
		if err != nil {
			return err
		}
		if code != 204 && code != 404 && code != 410 {
			return fmt.Errorf("Google old event delete HTTP %d", code)
		}
	}
	code, _, err := c.Call(ctx, tok.AccessToken, "POST", path, event)
	if err != nil {
		return err
	}
	if code == 409 {
		delete(event, "id")
		code, _, err = c.Call(ctx, tok.AccessToken, "PATCH", path+"/"+eventID, event)
	}
	if err != nil {
		return err
	}
	if code < 200 || code >= 300 {
		return fmt.Errorf("Google event HTTP %d", code)
	}
	_, err = w.DB.ExecContext(ctx, `INSERT INTO google_events(deadline_id,calendar_id,event_id) VALUES($1,$2,$3) ON CONFLICT(deadline_id) DO UPDATE SET calendar_id=excluded.calendar_id,event_id=excluded.event_id,synced_at=now()`, j.deadline, selected, eventID)
	return err
}
func deterministicEventID(deadline string) string {
	h := sha256.Sum256([]byte(strings.ToLower(deadline)))
	return hex.EncodeToString(h[:16])
}
