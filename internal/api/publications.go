package api

import (
	"database/sql"
	"net/http"

	"github.com/google/uuid"
)

const publicationColumns = `p.id::text,p.source_id,p.tribunal,p.judicial_unit,p.case_number,p.title,p.content_html,p.content_text,p.available_on::text,p.situation,p.source_url,up.is_read,up.analysis_status,up.llm_assisted`

func scanPublication(row interface{ Scan(...any) error }) (map[string]any, error) {
	var id, source, tribunal, unit, number, title, html, plain, day, situation, url, analysis string
	var read, llmAssisted bool
	err := row.Scan(&id, &source, &tribunal, &unit, &number, &title, &html, &plain, &day, &situation, &url, &read, &analysis, &llmAssisted)
	if err != nil {
		return nil, err
	}
	var provenance any
	if analysis != "PENDING" && analysis != "RUNNING" {
		provenance = llmAssisted
	}
	return map[string]any{"id": id, "sourceId": source, "tribunal": tribunal, "judicialUnit": unit, "caseNumber": number, "title": title, "contentHtml": html, "contentText": plain, "availableOn": day, "situation": situation, "sourceUrl": url, "isRead": read, "analysisStatus": analysis, "llmAssisted": provenance}, nil
}
func (s *Server) listPublications(w http.ResponseWriter, r *http.Request) {
	p, l := page(r)
	q := r.URL.Query()
	read := q.Get("read")
	if read != "" && read != "true" && read != "false" {
		fail(w, 400, "read must be true or false")
		return
	}
	rows, err := s.DB.QueryContext(r.Context(), `SELECT `+publicationColumns+` FROM user_publications up JOIN publications p ON p.id=up.publication_id WHERE up.user_id=$1 AND ($2='' OR p.content_text ILIKE '%'||$2||'%' OR p.case_number ILIKE '%'||$2||'%') AND ($3='' OR p.tribunal=$3) AND ($4='' OR up.is_read=($4='true')) ORDER BY p.available_on DESC,p.source_id DESC LIMIT $5 OFFSET $6`, user(r).ID, q.Get("q"), q.Get("tribunal"), read, l, (p-1)*l)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		v, e := scanPublication(rows)
		if e == nil {
			items = append(items, v)
		}
	}
	write(w, 200, map[string]any{"items": items, "page": p})
}
func (s *Server) analyzePublication(w http.ResponseWriter, r *http.Request) {
	if _, err := uuid.Parse(param(r, "id")); err != nil {
		fail(w, 400, "invalid publication id")
		return
	}
	res, err := s.DB.ExecContext(r.Context(), `UPDATE user_publications SET analysis_status='PENDING',analysis_attempts=0,analysis_started_at=NULL,analysis_next_attempt_at=NULL WHERE user_id=$1 AND publication_id=$2 AND analysis_requested_at IS NOT NULL AND analysis_status IN ('FAILED','RETRY')`, user(r).ID, param(r, "id"))
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		var status string
		var requested bool
		err := s.DB.QueryRowContext(r.Context(), `SELECT analysis_status,analysis_requested_at IS NOT NULL FROM user_publications WHERE user_id=$1 AND publication_id=$2`, user(r).ID, param(r, "id")).Scan(&status, &requested)
		if err == sql.ErrNoRows {
			fail(w, 404, "publication not found")
			return
		}
		if err != nil {
			fail(w, 500, "database error")
			return
		}
		if !requested {
			fail(w, 409, "publication analysis is not queued")
			return
		}
		write(w, 202, map[string]any{"status": status})
		return
	}
	write(w, 202, map[string]any{"status": "PENDING"})
}

func (s *Server) openPublication(w http.ResponseWriter, r *http.Request) {
	if _, err := uuid.Parse(param(r, "id")); err != nil {
		fail(w, 400, "invalid publication id")
		return
	}
	var status string
	var assisted bool
	err := s.DB.QueryRowContext(r.Context(), `UPDATE user_publications SET is_read=TRUE,analysis_requested_at=COALESCE(analysis_requested_at,now()) WHERE user_id=$1 AND publication_id=$2 RETURNING analysis_status,llm_assisted`, user(r).ID, param(r, "id")).Scan(&status, &assisted)
	if err == sql.ErrNoRows {
		fail(w, 404, "publication not found")
		return
	}
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	var provenance any
	if status != "PENDING" && status != "RUNNING" {
		provenance = assisted
	}
	write(w, 202, map[string]any{"isRead": true, "analysisStatus": status, "llmAssisted": provenance})
}
func (s *Server) getPublication(w http.ResponseWriter, r *http.Request) {
	v, err := scanPublication(s.DB.QueryRowContext(r.Context(), `SELECT `+publicationColumns+` FROM user_publications up JOIN publications p ON p.id=up.publication_id WHERE up.user_id=$1 AND p.id=$2`, user(r).ID, param(r, "id")))
	if err == sql.ErrNoRows {
		fail(w, 404, "publication not found")
		return
	}
	if err != nil {
		fail(w, 400, "invalid publication id")
		return
	}
	write(w, 200, v)
}
func (s *Server) setPublicationRead(w http.ResponseWriter, r *http.Request) {
	var b struct {
		IsRead bool `json:"isRead"`
	}
	if decode(r, &b) != nil {
		fail(w, 400, "invalid JSON")
		return
	}
	res, err := s.DB.ExecContext(r.Context(), `UPDATE user_publications SET is_read=$3 WHERE user_id=$1 AND publication_id=$2`, user(r).ID, param(r, "id"), b.IsRead)
	if err != nil {
		fail(w, 400, "invalid publication id")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		fail(w, 404, "publication not found")
		return
	}
	w.WriteHeader(204)
}
func (s *Server) linkPublication(w http.ResponseWriter, r *http.Request) {
	res, err := s.DB.ExecContext(r.Context(), `INSERT INTO case_publications(user_id,case_id,publication_id,manual) SELECT $1,c.id,up.publication_id,TRUE FROM cases c JOIN user_publications up ON up.user_id=c.user_id WHERE c.user_id=$1 AND c.id=$2 AND up.publication_id=$3 ON CONFLICT(user_id,case_id,publication_id) DO UPDATE SET manual=TRUE`, user(r).ID, param(r, "caseID"), param(r, "id"))
	if err != nil {
		fail(w, 400, "invalid IDs")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		fail(w, 404, "case or publication not found")
		return
	}
	w.WriteHeader(204)
}
func (s *Server) unlinkPublication(w http.ResponseWriter, r *http.Request) {
	res, err := s.DB.ExecContext(r.Context(), `DELETE FROM case_publications WHERE user_id=$1 AND case_id=$2 AND publication_id=$3`, user(r).ID, param(r, "caseID"), param(r, "id"))
	if err != nil {
		fail(w, 400, "invalid IDs")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		fail(w, 404, "link not found")
		return
	}
	w.WriteHeader(204)
}
func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	var unread, pending, upcoming, activeCases int
	_ = s.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM user_publications WHERE user_id=$1 AND NOT is_read`, user(r).ID).Scan(&unread)
	_ = s.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM deadline_suggestions WHERE user_id=$1 AND status IN ('PENDING','NEEDS_REVIEW')`, user(r).ID).Scan(&pending)
	_ = s.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM deadlines WHERE user_id=$1 AND status='ACTIVE' AND due_on BETWEEN current_date AND current_date+7`, user(r).ID).Scan(&upcoming)
	_ = s.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM cases WHERE user_id=$1 AND status NOT IN ('CLOSED','ARCHIVED')`, user(r).ID).Scan(&activeCases)
	write(w, 200, map[string]int{"unreadPublications": unread, "pendingSuggestions": pending, "upcomingDeadlines": upcoming, "activeCases": activeCases})
}
