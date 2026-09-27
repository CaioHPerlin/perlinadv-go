package worker

import (
	"context"
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
	id, number, uf string
	labor          bool
}

// pollLawyers repeats a short overlapping window so source edits are seen.
func (w Worker) pollLawyers(ctx context.Context, today time.Time) error {
	rows, err := w.DB.QueryContext(ctx, `SELECT id::text,oab_number,oab_uf,labor_practice FROM users WHERE active AND role='LAWYER' AND llm_consent AND oab_number IS NOT NULL AND oab_uf IS NOT NULL ORDER BY id`)
	if err != nil {
		return err
	}
	lawyers := []lawyerPoll{}
	for rows.Next() {
		var lawyer lawyerPoll
		if err := rows.Scan(&lawyer.id, &lawyer.number, &lawyer.uf, &lawyer.labor); err != nil {
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
	start := previousBusinessDays(today, 7)[6]
	end := today.Format("2006-01-02")
	var failures int
	for _, lawyer := range lawyers {
		tribunals := []string{"TJMS", "TRF3"}
		if lawyer.labor {
			tribunals = append(tribunals, "TRT24")
		}
		for _, tribunal := range tribunals {
			_, err := w.DB.ExecContext(ctx, `INSERT INTO djen_lawyer_syncs(user_id,tribunal,status,last_started_at,last_error) VALUES($1,$2,'PENDING',now(),'') ON CONFLICT(user_id,tribunal) DO UPDATE SET status='PENDING',last_started_at=now(),last_error=''`, lawyer.id, tribunal)
			if err != nil {
				return err
			}
			count, err := w.pollLawyerTribunal(ctx, lawyer, tribunal, start, end)
			if err != nil {
				failures++
				log.Printf("DJEN OAB poll %s: %v", tribunal, err)
				_, _ = w.DB.ExecContext(ctx, `UPDATE djen_lawyer_syncs SET status='FAILED',last_error=$3 WHERE user_id=$1 AND tribunal=$2`, lawyer.id, tribunal, err.Error())
				continue
			}
			_, err = w.DB.ExecContext(ctx, `UPDATE djen_lawyer_syncs SET status='DONE',last_succeeded_at=now(),item_count=$3,last_error='' WHERE user_id=$1 AND tribunal=$2`, lawyer.id, tribunal, count)
			if err != nil {
				return err
			}
		}
	}
	if failures != 0 {
		return fmt.Errorf("%d lawyer/tribunal polls failed", failures)
	}
	return nil
}

func (w Worker) pollLawyerTribunal(ctx context.Context, lawyer lawyerPoll, tribunal, start, end string) (int, error) {
	const pageSize = 50
	seen := 0
	for page := 1; page <= 1000; page++ {
		query := url.Values{
			"numeroOab":                  {lawyer.number},
			"ufOab":                      {lawyer.uf},
			"siglaTribunal":              {tribunal},
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
		if result.Count < 0 || result.Count > 50000 || len(result.Items) > pageSize {
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
		if seen >= result.Count {
			return seen, nil
		}
	}
	return seen, errors.New("DJEN pagination limit exceeded")
}
