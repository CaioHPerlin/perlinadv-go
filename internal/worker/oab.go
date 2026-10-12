package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"strconv"
	"time"
)

type lawyerPoll struct {
	id, number, uf, monitoringStart string
	labor                           bool
}

var errDJENResultLimit = errors.New("DJEN result limit reached")

// pollLawyers repeats a short overlapping window so source edits are seen.
func (w Worker) pollLawyers(ctx context.Context, today time.Time) error {
	rows, err := w.DB.QueryContext(ctx, `SELECT id::text,oab_number,oab_uf,labor_practice,(monitoring_started_at AT TIME ZONE 'America/Campo_Grande')::date::text FROM users WHERE active AND role='LAWYER' AND llm_consent AND oab_number IS NOT NULL AND oab_uf IS NOT NULL AND NOT EXISTS(SELECT 1 FROM lawyer_onboarding_syncs s WHERE s.user_id=users.id AND s.status IN ('PENDING','RUNNING')) ORDER BY id`)
	if err != nil {
		return err
	}
	lawyers := []lawyerPoll{}
	for rows.Next() {
		var lawyer lawyerPoll
		if err := rows.Scan(&lawyer.id, &lawyer.number, &lawyer.uf, &lawyer.labor, &lawyer.monitoringStart); err != nil {
			rows.Close()
			return err
		}
		lawyers = append(lawyers, lawyer)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var failures int
	for _, lawyer := range lawyers {
		tribunals := []string{"TJMS", "TRF3"}
		if lawyer.labor {
			tribunals = append(tribunals, "TRT24")
		}
		for _, tribunal := range tribunals {
			if _, err := w.pollLawyerDaily(ctx, lawyer, tribunal, today); err != nil {
				failures++
				log.Printf("DJEN OAB poll %s: %v", tribunal, err)
			}
		}
	}
	if failures != 0 {
		return fmt.Errorf("%d lawyer/tribunal polls failed", failures)
	}
	return nil
}

func (w Worker) pollLawyerDaily(ctx context.Context, lawyer lawyerPoll, tribunal string, today time.Time) (int, error) {
	today, _ = time.ParseInLocation("2006-01-02", today.Format("2006-01-02"), today.Location())
	_, err := w.DB.ExecContext(ctx, `INSERT INTO djen_lawyer_syncs(user_id,tribunal,status,last_started_at,last_error) VALUES($1,$2,'PENDING',now(),'') ON CONFLICT(user_id,tribunal) DO UPDATE SET status='PENDING',last_started_at=now(),last_error=''`, lawyer.id, tribunal)
	if err != nil {
		return 0, err
	}
	var covered, lastSuccess, retryFrom sql.NullTime
	if err = w.DB.QueryRowContext(ctx, `SELECT last_covered_on,last_succeeded_at,retry_from_on FROM djen_lawyer_syncs WHERE user_id=$1 AND tribunal=$2`, lawyer.id, tribunal).Scan(&covered, &lastSuccess, &retryFrom); err != nil {
		return 0, err
	}
	start, err := recurringStart(today, lawyer.monitoringStart, covered, lastSuccess, retryFrom)
	if err != nil {
		return 0, err
	}
	count := 0
	failedOn := ""
	for day := start; !day.After(today); {
		end := day.AddDate(0, 0, 13)
		if end.After(today) {
			end = today
		}
		failedOn = day.Format("2006-01-02")
		var n int
		n, err = w.pollLawyerRange(ctx, lawyer, tribunal, day, end)
		if err != nil {
			break
		}
		count += n
		if _, err = w.DB.ExecContext(ctx, `UPDATE djen_lawyer_syncs SET last_covered_on=GREATEST(last_covered_on,$3::date) WHERE user_id=$1 AND tribunal=$2`, lawyer.id, tribunal, end.Format("2006-01-02")); err != nil {
			break
		}
		day = end.AddDate(0, 0, 1)
	}
	if err != nil {
		failedCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = w.DB.ExecContext(failedCtx, `UPDATE djen_lawyer_syncs SET status='FAILED',last_error=$3,retry_from_on=$4::date WHERE user_id=$1 AND tribunal=$2`, lawyer.id, tribunal, err.Error(), failedOn)
		return count, err
	}
	_, err = w.DB.ExecContext(ctx, `UPDATE djen_lawyer_syncs SET status='DONE',last_succeeded_at=now(),last_covered_on=$4::date,retry_from_on=NULL,item_count=$3,last_error='' WHERE user_id=$1 AND tribunal=$2`, lawyer.id, tribunal, count, today.Format("2006-01-02"))
	return count, err
}

// A 14-day range keeps routine queries small. If DJEN reports its 10,000
// result ceiling, split the range; a single saturated day needs investigation.
func (w Worker) pollLawyerRange(ctx context.Context, lawyer lawyerPoll, tribunal string, start, end time.Time) (int, error) {
	count, err := w.pollLawyerTribunal(ctx, lawyer, tribunal, start.Format("2006-01-02"), end.Format("2006-01-02"))
	if !errors.Is(err, errDJENResultLimit) || start.Format("2006-01-02") == end.Format("2006-01-02") {
		return count, err
	}
	mid := start.AddDate(0, 0, int(end.Sub(start).Hours()/48))
	left, err := w.pollLawyerRange(ctx, lawyer, tribunal, start, mid)
	if err != nil {
		return left, err
	}
	right, err := w.pollLawyerRange(ctx, lawyer, tribunal, mid.AddDate(0, 0, 1), end)
	return left + right, err
}

// The first run of each day rechecks recent editions for source revisions.
// Failed runs resume from the last fully checked date, even after a long outage.
func recurringStart(today time.Time, monitoringStart string, covered, lastSuccess, retryFrom sql.NullTime) (time.Time, error) {
	registered, err := time.ParseInLocation("2006-01-02", monitoringStart, today.Location())
	if err != nil {
		return time.Time{}, err
	}
	first := previousBusinessDays(registered, 1)[0]
	start, _ := time.ParseInLocation("2006-01-02", first, today.Location())
	if retryFrom.Valid {
		return time.ParseInLocation("2006-01-02", retryFrom.Time.Format("2006-01-02"), today.Location())
	}
	if covered.Valid {
		coveredDay, _ := time.ParseInLocation("2006-01-02", covered.Time.Format("2006-01-02"), today.Location())
		start = coveredDay.AddDate(0, 0, 1)
	}
	if !lastSuccess.Valid || lastSuccess.Time.In(today.Location()).Format("2006-01-02") != today.Format("2006-01-02") {
		recheck, _ := time.ParseInLocation("2006-01-02", previousBusinessDays(today, 7)[6], today.Location())
		if recheck.Before(start) {
			start = recheck
		}
	} else if today.Before(start) {
		start = today
	}
	minimum, _ := time.ParseInLocation("2006-01-02", first, today.Location())
	if start.Before(minimum) {
		start = minimum
	}
	return start, nil
}

func (w Worker) pollLawyerTribunal(ctx context.Context, lawyer lawyerPoll, tribunal, start, end string) (int, error) {
	return w.pollLawyerTribunalWithProgress(ctx, lawyer, tribunal, start, end, nil)
}

func (w Worker) pollLawyerTribunalWithProgress(ctx context.Context, lawyer lawyerPoll, tribunal, start, end string, onPage func(seen, total int) error) (int, error) {
	const pageSize = 100
	seen := 0
	for page := 1; page <= 1000; page++ {
		query := url.Values{
			"numeroOab":                  {lawyer.number},
			"ufOab":                      {lawyer.uf},
			"siglaTribunal":              {tribunal},
			"meio":                       {"D"},
			"dataDisponibilizacaoInicio": {start},
			"dataDisponibilizacaoFim":    {end},
			"pagina":                     {strconv.Itoa(page)},
			"itensPorPagina":             {strconv.Itoa(pageSize)},
		}
		resp, err := w.get(ctx, w.BaseURL+"/comunicacao?"+query.Encode())
		if err != nil {
			return seen, err
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return seen, fmt.Errorf("communications HTTP %d", resp.StatusCode)
		}
		var result struct {
			Count int        `json:"count"`
			Items []djenItem `json:"items"`
		}
		decoder := json.NewDecoder(io.LimitReader(resp.Body, 32<<20))
		decoder.UseNumber()
		err = decoder.Decode(&result)
		resp.Body.Close()
		if err != nil {
			return seen, err
		}
		if result.Count >= 10000 {
			return seen, errDJENResultLimit
		}
		if result.Count < 0 || len(result.Items) > pageSize {
			return seen, errors.New("invalid DJEN pagination")
		}
		if len(result.Items) == 0 && seen < result.Count {
			return seen, errors.New("empty DJEN page before reported count")
		}
		for _, item := range result.Items {
			if item.Tribunal != tribunal {
				return seen, errors.New("DJEN tribunal filter mismatch")
			}
			if err := w.saveItem(ctx, item, tribunal, "oab-query"); err != nil {
				return seen, err
			}
			seen++
		}
		if onPage != nil {
			if err := onPage(seen, result.Count); err != nil {
				return seen, err
			}
		}
		if seen >= result.Count {
			return seen, nil
		}
	}
	return seen, errors.New("DJEN pagination limit exceeded")
}
