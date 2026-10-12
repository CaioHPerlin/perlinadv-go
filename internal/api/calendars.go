package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

type calendarDayBody struct {
	Day          string `json:"day"`
	Effect       string `json:"effect"`
	Title        string `json:"title"`
	AuthorityURL string `json:"authorityUrl"`
}
type calendarImportBody struct {
	Tribunal      string            `json:"tribunal"`
	ScopeType     string            `json:"scopeType"`
	ScopeKey      string            `json:"scopeKey"`
	CoverageStart string            `json:"coverageStart"`
	CoverageEnd   string            `json:"coverageEnd"`
	SourceTitle   string            `json:"sourceTitle"`
	SourceURL     string            `json:"sourceUrl"`
	Days          []calendarDayBody `json:"days"`
}

func officialURL(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "tjms.jus.br" || strings.HasSuffix(host, ".tjms.jus.br") || host == "trf3.jus.br" || strings.HasSuffix(host, ".trf3.jus.br") || host == "trt24.jus.br" || strings.HasSuffix(host, ".trt24.jus.br") || host == "planalto.gov.br" || strings.HasSuffix(host, ".planalto.gov.br")
}
func (s *Server) importCalendar(w http.ResponseWriter, r *http.Request) {
	var b calendarImportBody
	if decode(r, &b) != nil {
		fail(w, 400, "invalid JSON")
		return
	}
	b.Tribunal = strings.ToUpper(b.Tribunal)
	b.ScopeType = strings.ToUpper(b.ScopeType)
	b.ScopeKey = strings.ToUpper(strings.TrimSpace(b.ScopeKey))
	if (b.Tribunal != "TJMS" && b.Tribunal != "TRF3" && b.Tribunal != "TRT24") || (b.ScopeType != "TRIBUNAL" && b.ScopeType != "SECTION" && b.ScopeType != "COMARCA") || b.ScopeKey == "" || !validDate(b.CoverageStart) || !validDate(b.CoverageEnd) || b.CoverageEnd < b.CoverageStart || strings.TrimSpace(b.SourceTitle) == "" || !officialURL(b.SourceURL) {
		fail(w, 400, "invalid calendar metadata or nonofficial source")
		return
	}
	if b.ScopeType == "TRIBUNAL" && b.ScopeKey != b.Tribunal {
		fail(w, 400, "tribunal scope key must equal tribunal")
		return
	}
	if (b.ScopeType == "SECTION" && b.Tribunal != "TRF3") || (b.ScopeType == "COMARCA" && b.Tribunal == "TRF3") {
		fail(w, 400, "invalid tribunal and locality scope combination")
		return
	}
	for _, d := range b.Days {
		if !validDate(d.Day) || d.Day < b.CoverageStart || d.Day > b.CoverageEnd || (d.Effect != "CLOSED" && d.Effect != "DEADLINE_SUSPENDED" && d.Effect != "PARTIAL" && d.Effect != "INFO") || !officialURL(d.AuthorityURL) {
			fail(w, 400, "invalid calendar day")
			return
		}
	}
	raw, _ := json.Marshal(b)
	digest := sha256.Sum256(raw)
	sourceHash := hex.EncodeToString(digest[:])
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(r.Context(), `INSERT INTO court_calendar_versions(tribunal,scope_type,scope_key,coverage_start,coverage_end,source_title,source_url,source_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text`, b.Tribunal, b.ScopeType, b.ScopeKey, b.CoverageStart, b.CoverageEnd, b.SourceTitle, b.SourceURL, sourceHash).Scan(&id)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	for _, d := range b.Days {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO court_calendar_days(version_id,day,effect,title,authority_url) VALUES($1,$2,$3,$4,$5) ON CONFLICT(version_id,day,effect) DO UPDATE SET title=excluded.title,authority_url=excluded.authority_url`, id, d.Day, d.Effect, d.Title, d.AuthorityURL)
		if err != nil {
			fail(w, 500, "database error")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		fail(w, 500, "database error")
		return
	}
	write(w, 201, map[string]any{"id": id, "status": "DRAFT", "days": len(b.Days)})
}
func (s *Server) listCalendars(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.QueryContext(r.Context(), `SELECT id::text,tribunal,scope_type,scope_key,coverage_start::text,coverage_end::text,source_title,source_url,status FROM court_calendar_versions ORDER BY imported_at DESC LIMIT 200`)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	defer rows.Close()
	items := []map[string]string{}
	for rows.Next() {
		var id, t, st, sk, start, end, title, url, status string
		if rows.Scan(&id, &t, &st, &sk, &start, &end, &title, &url, &status) == nil {
			items = append(items, map[string]string{"id": id, "tribunal": t, "scopeType": st, "scopeKey": sk, "coverageStart": start, "coverageEnd": end, "sourceTitle": title, "sourceUrl": url, "status": status})
		}
	}
	write(w, 200, map[string]any{"items": items})
}
func (s *Server) approveCalendar(w http.ResponseWriter, r *http.Request) {
	var approval struct {
		VerifiedComplete bool `json:"verifiedComplete"`
	}
	if decode(r, &approval) != nil || !approval.VerifiedComplete {
		fail(w, 400, "verifiedComplete=true required after comparing with the official act")
		return
	}
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	defer tx.Rollback()
	var tribunal, start, end string
	err = tx.QueryRowContext(r.Context(), `UPDATE court_calendar_versions SET status='ACTIVE',coverage_verified=TRUE,approved_at=now() WHERE id=$1 AND status='DRAFT' RETURNING tribunal,coverage_start::text,coverage_end::text`, param(r, "id")).Scan(&tribunal, &start, &end)
	if err == sql.ErrNoRows {
		fail(w, 404, "draft calendar not found")
		return
	}
	if err != nil {
		fail(w, 400, "invalid calendar id")
		return
	}
	// Existing human decisions stay intact; affected dates require explicit re-review.
	_, err = tx.ExecContext(r.Context(), `UPDATE deadline_suggestions ds SET status='NEEDS_REVIEW',updated_at=now() FROM publications p WHERE p.id=ds.publication_id AND p.tribunal=$1 AND ds.suggested_due_on>=$2 AND ds.publication_on<=$3 AND ds.status='PENDING'`, tribunal, start, end)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE deadlines d SET needs_review=TRUE,updated_at=now() FROM publications p WHERE p.id=d.publication_id AND p.tribunal=$1 AND d.due_on>=$2 AND p.available_on<=$3`, tribunal, start, end)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE user_publications up SET analysis_status='PENDING',analysis_attempts=0,analysis_requested_at=COALESCE(analysis_requested_at,now()),analysis_started_at=NULL,analysis_next_attempt_at=NULL FROM publications p WHERE p.id=up.publication_id AND p.tribunal=$1 AND p.available_on<=$3 AND p.available_on+interval '900 days'>=$2 AND up.analysis_status<>'PENDING'`, tribunal, start, end)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 500, "database error")
		return
	}
	write(w, 200, map[string]string{"status": "ACTIVE"})
}
func (s *Server) syncStatus(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.QueryContext(r.Context(), `SELECT tribunal,max(edition_on)::text,count(*) FILTER (WHERE status='FAILED') FROM djen_editions GROUP BY tribunal ORDER BY tribunal`)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var t string
		var last sql.NullString
		var failures int
		if rows.Scan(&t, &last, &failures) == nil {
			items = append(items, map[string]any{"tribunal": t, "lastEdition": last.String, "failedEditions": failures})
		}
	}
	var analysisFailed, googleFailed, lawyerPollFailed int
	_ = s.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM user_publications WHERE analysis_status='FAILED'`).Scan(&analysisFailed)
	_ = s.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM google_jobs WHERE status='FAILED'`).Scan(&googleFailed)
	_ = s.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM djen_lawyer_syncs WHERE status='FAILED'`).Scan(&lawyerPollFailed)
	polls := []map[string]any{}
	pollRows, err := s.DB.QueryContext(r.Context(), `SELECT user_id::text,tribunal,status,coalesce(last_succeeded_at::text,''),coalesce(last_covered_on::text,''),coalesce(retry_from_on::text,''),item_count,last_error FROM djen_lawyer_syncs ORDER BY last_started_at DESC NULLS LAST LIMIT 100`)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	defer pollRows.Close()
	for pollRows.Next() {
		var userID, tribunal, status, lastSuccess, lastCovered, retryFrom, lastError string
		var count int
		if pollRows.Scan(&userID, &tribunal, &status, &lastSuccess, &lastCovered, &retryFrom, &count, &lastError) == nil {
			polls = append(polls, map[string]any{"userId": userID, "tribunal": tribunal, "status": status, "lastSucceededAt": lastSuccess, "lastCoveredOn": lastCovered, "retryFromOn": retryFrom, "itemCount": count, "lastError": lastError})
		}
	}
	write(w, 200, map[string]any{"items": items, "lawyerPolls": polls, "lawyerPollFailures": lawyerPollFailed, "analysisFailures": analysisFailed, "googleFailures": googleFailed})
}
