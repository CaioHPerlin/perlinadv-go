package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/caiohperlin/perlinadv/internal/store"
	"github.com/google/uuid"
)

type deadlineBody struct {
	CaseID      string `json:"caseId"`
	Title       string `json:"title"`
	Description string `json:"description"`
	DueOn       string `json:"dueOn"`
	Status      string `json:"status"`
}

func validDeadline(b *deadlineBody) bool {
	b.Title = strings.TrimSpace(b.Title)
	if b.Status == "" {
		b.Status = "ACTIVE"
	}
	return b.Title != "" && validDate(b.DueOn) && (b.Status == "ACTIVE" || b.Status == "COMPLETED" || b.Status == "CANCELED")
}

func optionalID(id uuid.NullUUID) string {
	if id.Valid {
		return id.UUID.String()
	}
	return ""
}
func deadlineValue(id uuid.UUID, caseID, publicationID, suggestionID uuid.NullUUID, title, description string, due time.Time, status string, review, llmAssisted bool) map[string]any {
	return map[string]any{"id": id.String(), "caseId": optionalID(caseID), "publicationId": optionalID(publicationID), "suggestionId": optionalID(suggestionID), "title": title, "description": description, "dueOn": due.Format("2006-01-02"), "status": status, "needsReview": review, "llmAssisted": llmAssisted}
}
func (s *Server) ownedCaseID(r *http.Request, raw string) (uuid.NullUUID, bool) {
	if raw == "" {
		return uuid.NullUUID{}, true
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.NullUUID{}, false
	}
	uid, _ := uuid.Parse(user(r).ID)
	exists, err := store.New(s.DB).CaseOwnedExists(r.Context(), store.CaseOwnedExistsParams{ID: id, UserID: uid})
	return uuid.NullUUID{UUID: id, Valid: true}, err == nil && exists
}
func (s *Server) listDeadlines(w http.ResponseWriter, r *http.Request) {
	p, l := page(r)
	uid, _ := uuid.Parse(user(r).ID)
	rows, err := store.New(s.DB).ListDeadlines(r.Context(), store.ListDeadlinesParams{UserID: uid, Limit: int32(l), Offset: int32((p - 1) * l)})
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	items := []map[string]any{}
	for _, v := range rows {
		items = append(items, deadlineValue(v.ID, v.CaseID, v.PublicationID, v.SuggestionID, v.Title, v.Description, v.DueOn, v.Status, v.NeedsReview, v.LlmAssisted))
	}
	write(w, 200, map[string]any{"items": items, "page": p})
}
func (s *Server) getDeadline(w http.ResponseWriter, r *http.Request) {
	id, parseErr := uuid.Parse(param(r, "id"))
	if parseErr != nil {
		fail(w, 400, "invalid deadline id")
		return
	}
	uid, _ := uuid.Parse(user(r).ID)
	v, err := store.New(s.DB).GetDeadline(r.Context(), store.GetDeadlineParams{ID: id, UserID: uid})
	if err == sql.ErrNoRows {
		fail(w, 404, "deadline not found")
		return
	}
	if err != nil {
		fail(w, 400, "invalid deadline id")
		return
	}
	write(w, 200, deadlineValue(v.ID, v.CaseID, v.PublicationID, v.SuggestionID, v.Title, v.Description, v.DueOn, v.Status, v.NeedsReview, v.LlmAssisted))
}
func (s *Server) createDeadline(w http.ResponseWriter, r *http.Request) {
	var b deadlineBody
	if decode(r, &b) != nil || !validDeadline(&b) {
		fail(w, 400, "title and valid dueOn required")
		return
	}
	caseID, owned := s.ownedCaseID(r, b.CaseID)
	if !owned {
		fail(w, 404, "case not found")
		return
	}
	uid, _ := uuid.Parse(user(r).ID)
	due, _ := time.Parse("2006-01-02", b.DueOn)
	created, err := store.New(s.DB).CreateDeadline(r.Context(), store.CreateDeadlineParams{UserID: uid, CaseID: caseID, Title: b.Title, Description: b.Description, DueOn: due, Status: b.Status})
	if err != nil {
		fail(w, 400, "invalid deadline")
		return
	}
	id := created.String()
	if b.Status == "ACTIVE" {
		s.queueGoogle(r, id, "UPSERT", "", "")
	}
	s.getDeadline(w, withParam(r, "id", id))
}
func (s *Server) updateDeadline(w http.ResponseWriter, r *http.Request) {
	var b deadlineBody
	if decode(r, &b) != nil || !validDeadline(&b) {
		fail(w, 400, "title and valid dueOn required")
		return
	}
	caseID, owned := s.ownedCaseID(r, b.CaseID)
	if !owned {
		fail(w, 404, "case not found")
		return
	}
	id, parseErr := uuid.Parse(param(r, "id"))
	if parseErr != nil {
		fail(w, 400, "invalid deadline id")
		return
	}
	uid, _ := uuid.Parse(user(r).ID)
	due, _ := time.Parse("2006-01-02", b.DueOn)
	n, err := store.New(s.DB).UpdateDeadline(r.Context(), store.UpdateDeadlineParams{ID: id, UserID: uid, CaseID: caseID, Title: b.Title, Description: b.Description, DueOn: due, Status: b.Status})
	if err != nil {
		fail(w, 400, "invalid deadline")
		return
	}
	if n == 0 {
		fail(w, 404, "deadline not found")
		return
	}
	if b.Status == "ACTIVE" {
		s.queueGoogle(r, param(r, "id"), "UPSERT", "", "")
	} else {
		s.queueDeleteEvent(r, param(r, "id"))
	}
	s.getDeadline(w, r)
}
func (s *Server) deleteDeadline(w http.ResponseWriter, r *http.Request) {
	id, parseErr := uuid.Parse(param(r, "id"))
	if parseErr != nil {
		fail(w, 400, "invalid deadline id")
		return
	}
	s.queueDeleteEvent(r, param(r, "id"))
	uid, _ := uuid.Parse(user(r).ID)
	n, err := store.New(s.DB).DeleteDeadline(r.Context(), store.DeleteDeadlineParams{ID: id, UserID: uid})
	if err != nil {
		fail(w, 400, "invalid deadline id")
		return
	}
	if n == 0 {
		fail(w, 404, "deadline not found")
		return
	}
	w.WriteHeader(204)
}

func (s *Server) listSuggestions(w http.ResponseWriter, r *http.Request) {
	p, l := page(r)
	rows, err := s.DB.QueryContext(r.Context(), `SELECT id::text,publication_id::text,action,evidence,coalesce(duration_days,0),unit,method,rule_version,status,coalesce(suggested_due_on::text,''),coalesce(publication_on::text,''),coalesce(first_counted_on::text,''),calculation FROM deadline_suggestions WHERE user_id=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, user(r).ID, l, (p-1)*l)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, pid, action, evidence, unit, method, version, status, due, pub, first string
		var days int
		var calc []byte
		if rows.Scan(&id, &pid, &action, &evidence, &days, &unit, &method, &version, &status, &due, &pub, &first, &calc) == nil {
			var audit any
			_ = json.Unmarshal(calc, &audit)
			items = append(items, map[string]any{"id": id, "publicationId": pid, "action": action, "evidence": evidence, "durationDays": days, "unit": unit, "method": method, "llmAssisted": method == "LLM", "ruleVersion": version, "status": status, "suggestedDueOn": due, "publicationOn": pub, "firstCountedOn": first, "calculation": audit})
		}
	}
	write(w, 200, map[string]any{"items": items, "page": p})
}
func (s *Server) confirmSuggestion(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Title       string `json:"title"`
		DueOn       string `json:"dueOn"`
		CaseID      string `json:"caseId"`
		Description string `json:"description"`
	}
	if decode(r, &b) != nil || !validDate(b.DueOn) || strings.TrimSpace(b.Title) == "" {
		fail(w, 400, "title and confirmed dueOn required")
		return
	}
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	defer tx.Rollback()
	var pid string
	err = tx.QueryRowContext(r.Context(), `SELECT publication_id::text FROM deadline_suggestions WHERE id=$1 AND user_id=$2 AND status IN ('PENDING','NEEDS_REVIEW') FOR UPDATE`, param(r, "id"), user(r).ID).Scan(&pid)
	if err != nil {
		fail(w, 404, "suggestion not available")
		return
	}
	if b.CaseID != "" {
		var exists bool
		_ = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM cases WHERE id=$1 AND user_id=$2)`, b.CaseID, user(r).ID).Scan(&exists)
		if !exists {
			fail(w, 404, "case not found")
			return
		}
	}
	var id string
	err = tx.QueryRowContext(r.Context(), `INSERT INTO deadlines(user_id,case_id,publication_id,suggestion_id,title,description,due_on) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id::text`, user(r).ID, nullable(b.CaseID), pid, param(r, "id"), b.Title, b.Description, b.DueOn).Scan(&id)
	if err != nil {
		fail(w, 409, "suggestion already confirmed")
		return
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE deadline_suggestions SET status='CONFIRMED',updated_at=now() WHERE id=$1`, param(r, "id"))
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO google_jobs(user_id,deadline_id,operation) SELECT $1,$2,'UPSERT' WHERE EXISTS(SELECT 1 FROM google_connections WHERE user_id=$1 AND disabled_at IS NULL AND calendar_id<>'')`, user(r).ID, id)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 500, "database error")
		return
	}
	s.getDeadline(w, withParam(r, "id", id))
}
func (s *Server) discardSuggestion(w http.ResponseWriter, r *http.Request) {
	res, err := s.DB.ExecContext(r.Context(), `UPDATE deadline_suggestions SET status='DISCARDED',updated_at=now() WHERE id=$1 AND user_id=$2 AND status IN ('PENDING','NEEDS_REVIEW')`, param(r, "id"), user(r).ID)
	if err != nil {
		fail(w, 400, "invalid suggestion id")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		fail(w, 404, "suggestion not found")
		return
	}
	w.WriteHeader(204)
}

func (s *Server) queueGoogle(r *http.Request, id, operation, eventID, calendarID string) {
	_, _ = s.DB.ExecContext(r.Context(), `INSERT INTO google_jobs(user_id,deadline_id,operation,event_id,calendar_id) SELECT $1,$2,$3,$4,$5 WHERE EXISTS(SELECT 1 FROM google_connections WHERE user_id=$1 AND disabled_at IS NULL AND calendar_id<>'')`, user(r).ID, id, operation, eventID, calendarID)
}
func (s *Server) queueDeleteEvent(r *http.Request, id string) {
	_, _ = s.DB.ExecContext(r.Context(), `INSERT INTO google_jobs(user_id,deadline_id,operation,event_id,calendar_id) SELECT $1,$2,'DELETE',ge.event_id,ge.calendar_id FROM google_events ge JOIN deadlines d ON d.id=ge.deadline_id WHERE d.id=$2 AND d.user_id=$1`, user(r).ID, id)
}
