package api

import (
	"net/http"
)

func (s *Server) onboardingPublicationStatus(w http.ResponseWriter, r *http.Request) {
	p := user(r)
	if p.Role != "LAWYER" {
		fail(w, 403, "lawyer account required")
		return
	}
	// Existing sessions may predate this feature. Creating the row is idempotent.
	if _, err := s.DB.ExecContext(r.Context(), `INSERT INTO lawyer_onboarding_syncs(user_id) VALUES($1) ON CONFLICT(user_id) DO NOTHING`, p.ID); err != nil {
		fail(w, 500, "database error")
		return
	}
	var status, tribunal string
	var progress, count int
	var acknowledged bool
	err := s.DB.QueryRowContext(r.Context(), `SELECT status,progress,current_tribunal,publication_count,acknowledged_at IS NOT NULL FROM lawyer_onboarding_syncs WHERE user_id=$1`, p.ID).Scan(&status, &progress, &tribunal, &count, &acknowledged)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	write(w, 200, map[string]any{"status": status, "progress": progress, "currentTribunal": tribunal, "publicationCount": count, "acknowledged": acknowledged})
}

func (s *Server) retryOnboardingPublications(w http.ResponseWriter, r *http.Request) {
	if user(r).Role != "LAWYER" {
		fail(w, 403, "lawyer account required")
		return
	}
	result, err := s.DB.ExecContext(r.Context(), `UPDATE lawyer_onboarding_syncs SET status='PENDING',progress=0,current_tribunal='',publication_count=0,last_error='',started_at=NULL,completed_at=NULL,acknowledged_at=NULL,updated_at=now() WHERE user_id=$1 AND status='FAILED'`, user(r).ID)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		fail(w, 409, "publication sync is not failed")
		return
	}
	write(w, 202, map[string]string{"status": "PENDING"})
}

func (s *Server) acknowledgeOnboardingPublications(w http.ResponseWriter, r *http.Request) {
	if user(r).Role != "LAWYER" {
		fail(w, 403, "lawyer account required")
		return
	}
	result, err := s.DB.ExecContext(r.Context(), `UPDATE lawyer_onboarding_syncs SET acknowledged_at=COALESCE(acknowledged_at,now()),updated_at=now() WHERE user_id=$1 AND status IN ('DONE','FAILED')`, user(r).ID)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		fail(w, 409, "publication sync is not finished")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
