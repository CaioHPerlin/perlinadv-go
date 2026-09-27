package calendar

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Day struct {
	Date    string   `json:"date"`
	Counted bool     `json:"counted"`
	Reason  string   `json:"reason"`
	Sources []string `json:"sources"`
}
type Result struct {
	PublicationOn  string      `json:"publicationOn"`
	FirstCountedOn string      `json:"firstCountedOn"`
	DueOn          string      `json:"dueOn"`
	Days           []Day       `json:"days"`
	VersionIDs     []string    `json:"versionIds"`
	Authorities    []Authority `json:"authorities,omitempty"`
	RuleVersion    string      `json:"ruleVersion"`
}
type Authority struct {
	VersionID string `json:"versionId"`
	Title     string `json:"title"`
	URL       string `json:"url"`
}

const RuleVersion = "djen-business-days-v1"

var ErrCoverage = errors.New("approved calendar coverage is incomplete")

type Checker interface {
	Check(context.Context, time.Time, string, string) (bool, string, []string, error)
}
type DatabaseChecker struct{ DB *sql.DB }

func (c DatabaseChecker) Check(ctx context.Context, day time.Time, tribunal, locality string) (bool, string, []string, error) {
	if locality == "" {
		return false, "unknown locality", nil, ErrCoverage
	}
	scope := "COMARCA"
	if tribunal == "TRF3" {
		scope = "SECTION"
	}
	rows, err := c.DB.QueryContext(ctx, `WITH latest AS (SELECT DISTINCT ON (scope_type,scope_key) id,scope_type,source_url FROM court_calendar_versions WHERE tribunal=$2 AND status='ACTIVE' AND coverage_verified AND coverage_start<=$1 AND coverage_end>=$1 AND ((scope_type='TRIBUNAL' AND scope_key=$2) OR (scope_type=$3 AND scope_key=$4)) ORDER BY scope_type,scope_key,approved_at DESC) SELECT v.id::text,v.scope_type,coalesce(string_agg(d.effect,','),''),v.source_url FROM latest v LEFT JOIN court_calendar_days d ON d.version_id=v.id AND d.day=$1 GROUP BY v.id,v.scope_type,v.source_url`, day.Format("2006-01-02"), tribunal, scope, locality)
	if err != nil {
		return false, "", nil, err
	}
	defer rows.Close()
	seenTribunal, seenLocal := false, false
	versions := []string{}
	closed := false
	reason := "business day"
	for rows.Next() {
		var id, kind, effects, url string
		if err = rows.Scan(&id, &kind, &effects, &url); err != nil {
			return false, "", nil, err
		}
		versions = append(versions, id)
		if kind == "TRIBUNAL" {
			seenTribunal = true
		} else {
			seenLocal = true
		}
		if effects != "" {
			for _, e := range splitEffects(effects) {
				if e == "CLOSED" || e == "DEADLINE_SUSPENDED" {
					closed = true
					reason = e
				}
			}
		}
	}
	if err = rows.Err(); err != nil {
		return false, "", nil, err
	}
	if !seenTribunal || !seenLocal {
		return false, "missing tribunal or locality coverage", versions, ErrCoverage
	}
	if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
		return false, "weekend", versions, nil
	}
	return !closed, reason, versions, nil
}
func splitEffects(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
func Calculate(ctx context.Context, c Checker, availableOn, tribunal, locality string, duration int) (Result, error) {
	if duration < 1 || duration > 365 {
		return Result{}, errors.New("duration must be 1..365 business days")
	}
	available, err := time.Parse("2006-01-02", availableOn)
	if err != nil {
		return Result{}, err
	}
	result := Result{Days: []Day{}, VersionIDs: []string{}, RuleVersion: RuleVersion}
	seen := map[string]bool{}
	count := 0
	phase := 0
	for day, step := available, 0; step < 900; step, day = step+1, day.AddDate(0, 0, 1) {
		if step == 0 {
			continue
		} // DJEN availability is never itself the publication day.
		ok, reason, ids, err := c.Check(ctx, day, tribunal, locality)
		if err != nil {
			return Result{}, fmt.Errorf("%w on %s", err, day.Format("2006-01-02"))
		}
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				result.VersionIDs = append(result.VersionIDs, id)
			}
		}
		entry := Day{Date: day.Format("2006-01-02"), Reason: reason, Sources: ids}
		if phase == 0 {
			if ok {
				result.PublicationOn = entry.Date
				phase = 1
				entry.Reason = "DJEN publication"
			}
		} else if ok {
			if phase == 1 {
				result.FirstCountedOn = entry.Date
				phase = 2
			}
			count++
			entry.Counted = true
			entry.Reason = "business day"
		}
		result.Days = append(result.Days, entry)
		if count == duration {
			result.DueOn = entry.Date
			return result, nil
		}
	}
	return Result{}, ErrCoverage
}
func JSON(r Result) []byte { b, _ := json.Marshal(r); return b }
