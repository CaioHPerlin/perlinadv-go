package api

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/caiohperlin/perlinadv/internal/store"
	"github.com/google/uuid"
)

type caseBody struct {
	CaseNumber   string `json:"caseNumber"`
	FolderNumber string `json:"folderNumber"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Status       string `json:"status"`
	Notes        string `json:"notes"`
	Tribunal     string `json:"tribunal"`
	Locality     string `json:"locality"`
	JudicialUnit string `json:"judicialUnit"`
}

func cleanCase(b *caseBody) bool {
	b.CaseNumber = strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, b.CaseNumber)
	b.Title = strings.TrimSpace(b.Title)
	b.Tribunal = strings.ToUpper(strings.TrimSpace(b.Tribunal))
	b.Locality = strings.ToUpper(strings.TrimSpace(b.Locality))
	if b.Status == "" {
		b.Status = "IN_PROGRESS"
	}
	return len(b.CaseNumber) == 20 && b.Title != "" && (b.Status == "IN_PROGRESS" || b.Status == "SUSPENDED" || b.Status == "ARCHIVED" || b.Status == "CLOSED") && (b.Tribunal == "" || b.Tribunal == "TJMS" || b.Tribunal == "TRF3" || b.Tribunal == "TRT24")
}

func caseValue(id uuid.UUID, n, folder, title, desc, status, notes, tribunal, locality, unit string) map[string]any {
	return map[string]any{"id": id.String(), "caseNumber": n, "folderNumber": folder, "title": title, "description": desc, "status": status, "notes": notes, "tribunal": tribunal, "locality": locality, "judicialUnit": unit}
}
func (s *Server) listCases(w http.ResponseWriter, r *http.Request) {
	p, l := page(r)
	uid, _ := uuid.Parse(user(r).ID)
	rows, err := store.New(s.DB).ListCases(r.Context(), store.ListCasesParams{UserID: uid, StatusFilter: r.URL.Query().Get("status"), Search: r.URL.Query().Get("q"), LimitCount: int32(l), OffsetCount: int32((p - 1) * l)})
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	items := []map[string]any{}
	for _, v := range rows {
		items = append(items, caseValue(v.ID, v.CaseNumber, v.FolderNumber, v.Title, v.Description, v.Status, v.Notes, v.Tribunal, v.Locality, v.JudicialUnit))
	}
	write(w, 200, map[string]any{"items": items, "page": p})
}
func (s *Server) createCase(w http.ResponseWriter, r *http.Request) {
	var b caseBody
	if decode(r, &b) != nil || !cleanCase(&b) {
		fail(w, 400, "valid CNJ case number, title and status required")
		return
	}
	uid, _ := uuid.Parse(user(r).ID)
	created, err := store.New(s.DB).CreateCase(r.Context(), store.CreateCaseParams{UserID: uid, CaseNumber: b.CaseNumber, FolderNumber: b.FolderNumber, Title: b.Title, Description: b.Description, Status: b.Status, Notes: b.Notes, Tribunal: b.Tribunal, Locality: b.Locality, JudicialUnit: b.JudicialUnit})
	if err != nil {
		fail(w, 409, "duplicate or invalid case")
		return
	}
	id := created.String()
	_, _ = s.DB.ExecContext(r.Context(), `INSERT INTO case_publications(user_id,case_id,publication_id) SELECT $1,$2,p.id FROM publications p JOIN user_publications up ON up.publication_id=p.id AND up.user_id=$1 WHERE p.case_number=$3 ON CONFLICT DO NOTHING`, user(r).ID, id, b.CaseNumber)
	s.reanalyzeCasePublications(r, b.CaseNumber)
	r2 := r.Clone(r.Context())
	r2 = withParam(r2, "id", id)
	s.getCase(w, r2)
}
func (s *Server) getCase(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(param(r, "id"))
	if err != nil {
		fail(w, 400, "invalid case id")
		return
	}
	uid, _ := uuid.Parse(user(r).ID)
	v, err := store.New(s.DB).GetCase(r.Context(), store.GetCaseParams{ID: id, UserID: uid})
	if err == sql.ErrNoRows {
		fail(w, 404, "case not found")
		return
	}
	if err != nil {
		fail(w, 400, "invalid case id")
		return
	}
	write(w, 200, caseValue(v.ID, v.CaseNumber, v.FolderNumber, v.Title, v.Description, v.Status, v.Notes, v.Tribunal, v.Locality, v.JudicialUnit))
}
func (s *Server) updateCase(w http.ResponseWriter, r *http.Request) {
	var b caseBody
	if decode(r, &b) != nil || !cleanCase(&b) {
		fail(w, 400, "valid CNJ case number, title and status required")
		return
	}
	id, parseErr := uuid.Parse(param(r, "id"))
	if parseErr != nil {
		fail(w, 400, "invalid case id")
		return
	}
	uid, _ := uuid.Parse(user(r).ID)
	n, err := store.New(s.DB).UpdateCase(r.Context(), store.UpdateCaseParams{ID: id, UserID: uid, CaseNumber: b.CaseNumber, FolderNumber: b.FolderNumber, Title: b.Title, Description: b.Description, Status: b.Status, Notes: b.Notes, Tribunal: b.Tribunal, Locality: b.Locality, JudicialUnit: b.JudicialUnit})
	if err != nil {
		fail(w, 409, "duplicate or invalid case")
		return
	}
	if n == 0 {
		fail(w, 404, "case not found")
		return
	}
	s.reanalyzeCasePublications(r, b.CaseNumber)
	s.getCase(w, r)
}
func (s *Server) reanalyzeCasePublications(r *http.Request, caseNumber string) {
	_, _ = s.DB.ExecContext(r.Context(), `UPDATE user_publications up SET analysis_status='PENDING',analysis_attempts=0,analysis_requested_at=COALESCE(analysis_requested_at,now()),analysis_started_at=NULL,analysis_next_attempt_at=NULL FROM publications p WHERE p.id=up.publication_id AND up.user_id=$1 AND p.case_number=$2 AND up.analysis_status<>'PENDING'`, user(r).ID, caseNumber)
}
func (s *Server) deleteCase(w http.ResponseWriter, r *http.Request) {
	id, parseErr := uuid.Parse(param(r, "id"))
	if parseErr != nil {
		fail(w, 400, "invalid case id")
		return
	}
	uid, _ := uuid.Parse(user(r).ID)
	n, err := store.New(s.DB).DeleteCase(r.Context(), store.DeleteCaseParams{ID: id, UserID: uid})
	if err != nil {
		fail(w, 400, "invalid case id")
		return
	}
	if n == 0 {
		fail(w, 404, "case not found")
		return
	}
	w.WriteHeader(204)
}
