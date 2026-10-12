package worker

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// ProcessOnboardingSync claims one first-login job and reports progress from
// actual DJEN pages. The regular hourly poll skips accounts with active jobs.
func (w Worker) ProcessOnboardingSync(ctx context.Context) error {
	return w.processOnboardingSync(ctx, "")
}

func (w Worker) processOnboardingSync(ctx context.Context, onlyUserID string) error {
	var lawyer lawyerPoll
	err := w.DB.QueryRowContext(ctx, `WITH next AS (
		SELECT s.user_id FROM lawyer_onboarding_syncs s JOIN users u ON u.id=s.user_id
		WHERE u.active AND u.role='LAWYER' AND u.llm_consent
		  AND ($1='' OR s.user_id::text=$1)
		  AND (s.status='PENDING' OR (s.status='RUNNING' AND s.updated_at<now()-interval '15 minutes'))
		ORDER BY s.updated_at FOR UPDATE OF s SKIP LOCKED LIMIT 1
	)
	UPDATE lawyer_onboarding_syncs s SET status='RUNNING',progress=0,current_tribunal='',publication_count=0,
		last_error='',started_at=now(),completed_at=NULL,updated_at=now()
	FROM next WHERE s.user_id=next.user_id RETURNING s.user_id::text`, onlyUserID).Scan(&lawyer.id)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if err = w.DB.QueryRowContext(ctx, `SELECT oab_number,oab_uf,labor_practice FROM users WHERE id=$1`, lawyer.id).Scan(&lawyer.number, &lawyer.uf, &lawyer.labor); err != nil {
		return w.failOnboarding(lawyer.id, err)
	}
	zone, _ := time.LoadLocation("America/Campo_Grande")
	today := time.Now().In(zone)
	days := onboardingDays(today)
	// TRT24 is optional coverage and runs in the recurring collector; it does
	// not block the first-login result for the two primary tribunals.
	tribunals := []string{"TJMS", "TRF3"}
	for index, tribunal := range tribunals {
		base := index * 100 / len(tribunals)
		if _, err = w.DB.ExecContext(ctx, `UPDATE lawyer_onboarding_syncs SET current_tribunal=$2,progress=GREATEST(progress,$3),updated_at=now() WHERE user_id=$1 AND status='RUNNING'`, lawyer.id, tribunal, base); err != nil {
			return w.failOnboarding(lawyer.id, err)
		}
		onPage := func(seen, total int) error {
			fraction := 1.0
			if total > 0 {
				fraction = float64(seen) / float64(total)
			}
			progress := int((float64(index) + fraction) * 100 / float64(len(tribunals)))
			if progress > 99 {
				progress = 99
			}
			_, updateErr := w.DB.ExecContext(ctx, `UPDATE lawyer_onboarding_syncs SET progress=GREATEST(progress,$2),updated_at=now() WHERE user_id=$1 AND status='RUNNING'`, lawyer.id, progress)
			return updateErr
		}
		if _, err = w.pollLawyerTribunalWithProgress(ctx, lawyer, tribunal, days[0], days[len(days)-1], onPage); err != nil {
			return w.failOnboarding(lawyer.id, fmt.Errorf("%s %s..%s: %w", tribunal, days[0], days[len(days)-1], err))
		}
	}
	var count int
	if err = w.DB.QueryRowContext(ctx, `SELECT count(*) FROM user_publications up JOIN publications p ON p.id=up.publication_id WHERE up.user_id=$1 AND p.available_on BETWEEN $2 AND $3`, lawyer.id, days[0], days[len(days)-1]).Scan(&count); err != nil {
		return w.failOnboarding(lawyer.id, err)
	}
	_, err = w.DB.ExecContext(ctx, `UPDATE lawyer_onboarding_syncs SET status='DONE',progress=100,current_tribunal='',publication_count=$2,completed_at=now(),updated_at=now() WHERE user_id=$1 AND status='RUNNING'`, lawyer.id, count)
	if err != nil {
		return w.failOnboarding(lawyer.id, err)
	}
	return nil
}

// Include both calendar yesterday and the previous business day. On Mondays
// that also includes Friday, while duplicate dates are queried only once.
func onboardingDays(today time.Time) []string {
	dates := map[string]bool{
		today.Format("2006-01-02"):                   true,
		today.AddDate(0, 0, -1).Format("2006-01-02"): true,
		previousBusinessDays(today, 1)[0]:            true,
	}
	days := make([]string, 0, len(dates))
	for day := range dates {
		days = append(days, day)
	}
	sort.Strings(days)
	return days
}

func (w Worker) failOnboarding(userID string, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = w.DB.ExecContext(ctx, `UPDATE lawyer_onboarding_syncs SET status='FAILED',current_tribunal='',last_error=$2,completed_at=now(),updated_at=now() WHERE user_id=$1 AND status='RUNNING'`, userID, cause.Error())
	return cause
}
