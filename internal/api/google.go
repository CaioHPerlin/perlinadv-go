package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/caiohperlin/perlinadv/internal/google"
)

func (s *Server) googleCallbackResult(w http.ResponseWriter, r *http.Request, connected bool, status int, message string) {
	result := "error"
	if connected {
		result = "connected"
	}
	target, err := frontendURL("/calendar", url.Values{"google": {result}})
	if err != nil {
		fail(w, 500, "frontend URL misconfigured")
		return
	}
	if target != "" {
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	if connected {
		write(w, 200, map[string]string{"status": "connected", "next": "GET /v1/google/calendars then PUT /v1/google/calendar"})
		return
	}
	fail(w, status, message)
}

func (s *Server) googleConnect(w http.ResponseWriter, r *http.Request) {
	c, err := google.New()
	if err != nil || !c.Configured() {
		fail(w, 503, "Google OAuth not configured")
		return
	}
	state, err := token()
	if err != nil {
		fail(w, 500, "state error")
		return
	}
	_, err = s.DB.ExecContext(r.Context(), `INSERT INTO google_oauth_states(state_hash,user_id,expires_at) VALUES($1,$2,now()+interval '10 minutes')`, hash(state), user(r).ID)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	write(w, 200, map[string]string{"authorizationUrl": c.AuthURL(state)})
}
func (s *Server) googleCallback(w http.ResponseWriter, r *http.Request) {
	state, code := r.URL.Query().Get("state"), r.URL.Query().Get("code")
	if state == "" || code == "" {
		s.googleCallbackResult(w, r, false, 400, "missing OAuth state or code")
		return
	}
	c, err := google.New()
	if err != nil || !c.Configured() {
		s.googleCallbackResult(w, r, false, 503, "Google OAuth not configured")
		return
	}
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		s.googleCallbackResult(w, r, false, 500, "database error")
		return
	}
	defer tx.Rollback()
	var uid string
	err = tx.QueryRowContext(r.Context(), `DELETE FROM google_oauth_states WHERE state_hash=$1 AND expires_at>now() RETURNING user_id::text`, hash(state)).Scan(&uid)
	if err != nil {
		s.googleCallbackResult(w, r, false, 400, "invalid OAuth state")
		return
	}
	t, err := c.Exchange(r.Context(), code)
	if err != nil || t.RefreshToken == "" {
		s.googleCallbackResult(w, r, false, 502, "Google did not provide offline access")
		return
	}
	cipher, err := c.Encrypt(t.RefreshToken)
	if err != nil {
		s.googleCallbackResult(w, r, false, 500, "encryption error")
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO google_connections(user_id,refresh_token_cipher) VALUES($1,$2) ON CONFLICT(user_id) DO UPDATE SET refresh_token_cipher=excluded.refresh_token_cipher,disabled_at=NULL,connected_at=now()`, uid, cipher)
	if err != nil {
		s.googleCallbackResult(w, r, false, 500, "database error")
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO google_jobs(user_id,deadline_id,operation) SELECT d.user_id,d.id,'UPSERT' FROM deadlines d JOIN google_connections gc ON gc.user_id=d.user_id WHERE d.user_id=$1 AND d.status='ACTIVE' AND d.needs_review=FALSE AND gc.calendar_id<>''`, uid)
	if err != nil {
		s.googleCallbackResult(w, r, false, 500, "database error")
		return
	}
	if err = tx.Commit(); err != nil {
		s.googleCallbackResult(w, r, false, 500, "database error")
		return
	}
	s.googleCallbackResult(w, r, true, 0, "")
}
func (s *Server) googleStatus(w http.ResponseWriter, r *http.Request) {
	var calendar string
	err := s.DB.QueryRowContext(r.Context(), `SELECT calendar_id FROM google_connections WHERE user_id=$1 AND disabled_at IS NULL`, user(r).ID).Scan(&calendar)
	if err == sql.ErrNoRows {
		write(w, 200, map[string]any{"connected": false})
		return
	}
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	write(w, 200, map[string]any{"connected": true, "calendarId": calendar})
}
func (s *Server) googleAccess(r *http.Request) (google.Client, string, error) {
	c, err := google.New()
	if err != nil {
		return c, "", err
	}
	var cipher []byte
	err = s.DB.QueryRowContext(r.Context(), `SELECT refresh_token_cipher FROM google_connections WHERE user_id=$1 AND disabled_at IS NULL`, user(r).ID).Scan(&cipher)
	if err != nil {
		return c, "", err
	}
	refresh, err := c.Decrypt(cipher)
	if err != nil {
		return c, "", err
	}
	t, err := c.Refresh(r.Context(), refresh)
	return c, t.AccessToken, err
}
func (s *Server) googleCalendars(w http.ResponseWriter, r *http.Request) {
	c, access, err := s.googleAccess(r)
	if err != nil {
		fail(w, 503, "Google connection unavailable")
		return
	}
	status, data, err := c.Call(r.Context(), access, "GET", "/users/me/calendarList", nil)
	if err != nil || status != 200 {
		fail(w, 502, "Google Calendar request failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
func (s *Server) googleSelectCalendar(w http.ResponseWriter, r *http.Request) {
	var b struct {
		CalendarID string `json:"calendarId"`
	}
	if decode(r, &b) != nil || strings.TrimSpace(b.CalendarID) == "" {
		fail(w, 400, "calendarId required")
		return
	}
	c, access, err := s.googleAccess(r)
	if err != nil {
		fail(w, 503, "Google connection unavailable")
		return
	}
	status, data, err := c.Call(r.Context(), access, "GET", "/users/me/calendarList/"+url.PathEscape(b.CalendarID), nil)
	if err != nil || status != 200 {
		fail(w, 400, "calendar not available")
		return
	}
	var cal struct {
		AccessRole string `json:"accessRole"`
	}
	if json.Unmarshal(data, &cal) != nil || (cal.AccessRole != "owner" && cal.AccessRole != "writer") {
		fail(w, 403, "calendar is not writable")
		return
	}
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(r.Context(), `INSERT INTO google_jobs(user_id,operation,event_id,calendar_id) SELECT $1,'DELETE',ge.event_id,ge.calendar_id FROM google_events ge JOIN deadlines d ON d.id=ge.deadline_id WHERE d.user_id=$1 AND ge.calendar_id<>$2`, user(r).ID, b.CalendarID)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE google_connections SET calendar_id=$2 WHERE user_id=$1`, user(r).ID, b.CalendarID)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO google_jobs(user_id,deadline_id,operation) SELECT user_id,id,'UPSERT' FROM deadlines WHERE user_id=$1 AND status='ACTIVE' AND needs_review=FALSE`, user(r).ID)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 500, "database error")
		return
	}
	write(w, 200, map[string]string{"calendarId": b.CalendarID})
}
func (s *Server) googleDisconnect(w http.ResponseWriter, r *http.Request) {
	_, err := s.DB.ExecContext(r.Context(), `UPDATE google_connections SET disabled_at=now() WHERE user_id=$1`, user(r).ID)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	w.WriteHeader(204)
}
