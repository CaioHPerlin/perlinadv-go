package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/caiohperlin/perlinadv/internal/store"
)

type registerBody struct {
	Name          string `json:"name"`
	Email         string `json:"email"`
	Password      string `json:"password"`
	OABNumber     string `json:"oabNumber"`
	OABUF         string `json:"oabUf"`
	LaborPractice bool   `json:"laborPractice"`
	LLMConsent    bool   `json:"llmConsent"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var b registerBody
	if decode(r, &b) != nil {
		fail(w, 400, "invalid JSON")
		return
	}
	b.Name = strings.TrimSpace(b.Name)
	b.Email = strings.ToLower(strings.TrimSpace(b.Email))
	b.OABNumber = normOAB(b.OABNumber)
	b.OABUF = strings.ToUpper(strings.TrimSpace(b.OABUF))
	address, emailErr := mail.ParseAddress(b.Email)
	if b.Name == "" || emailErr != nil || address.Address != b.Email || len(b.Password) < 12 || b.OABNumber == "" || len(b.OABUF) != 2 || b.OABUF[0] < 'A' || b.OABUF[0] > 'Z' || b.OABUF[1] < 'A' || b.OABUF[1] > 'Z' {
		fail(w, 400, "name, email, password (12+ characters), OAB and UF required")
		return
	}
	if !b.LLMConsent {
		fail(w, 400, "llmConsent=true is required to create an account")
		return
	}
	ph, err := bcrypt.GenerateFromPassword([]byte(b.Password), bcrypt.DefaultCost)
	if err != nil {
		fail(w, 500, "password error")
		return
	}
	created, err := store.New(s.DB).CreateLawyer(r.Context(), store.CreateLawyerParams{Name: b.Name, Email: b.Email, PasswordHash: string(ph), OabNumber: sql.NullString{String: b.OABNumber, Valid: true}, OabUf: sql.NullString{String: b.OABUF, Valid: true}, LaborPractice: b.LaborPractice, LlmConsent: b.LLMConsent})
	if err != nil {
		fail(w, 409, "email or OAB already registered")
		return
	}
	id := created.String()
	// A new lawyer receives yesterday's publications already present in the short buffer.
	_, _ = s.DB.ExecContext(r.Context(), `INSERT INTO user_publications(user_id,publication_id) SELECT $1,p.id FROM recent_publication_matches m JOIN publications p ON p.source_id=m.source_id WHERE m.oab_uf=$2 AND m.oab_number=$3 AND m.available_on=(now() AT TIME ZONE 'America/Campo_Grande')::date-1 AND m.expires_at>now() AND ($4 OR p.tribunal<>'TRT24') ON CONFLICT DO NOTHING`, id, b.OABUF, b.OABNumber, b.LaborPractice)
	write(w, 201, map[string]string{"id": id})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if decode(r, &b) != nil {
		fail(w, 400, "invalid JSON")
		return
	}
	account, err := store.New(s.DB).GetLoginUser(r.Context(), strings.ToLower(strings.TrimSpace(b.Email)))
	if err != nil || bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), []byte(b.Password)) != nil {
		fail(w, 401, "invalid credentials")
		return
	}
	if account.Role == "LAWYER" {
		if _, err = s.DB.ExecContext(r.Context(), `INSERT INTO lawyer_onboarding_syncs(user_id) VALUES($1) ON CONFLICT(user_id) DO NOTHING`, account.ID); err != nil {
			fail(w, 500, "could not prepare publication sync")
			return
		}
	}
	t, err := token()
	if err != nil {
		fail(w, 500, "token error")
		return
	}
	err = store.New(s.DB).CreateSession(r.Context(), store.CreateSessionParams{UserID: account.ID, TokenHash: hash(t)})
	if err != nil {
		fail(w, 500, "session error")
		return
	}
	write(w, 200, map[string]any{"token": t, "expiresInSeconds": 604800, "role": account.Role, "consentRequired": account.Role != "ADMIN" && !account.LlmConsent})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	t := strings.Fields(r.Header.Get("Authorization"))[1]
	_ = store.New(s.DB).RevokeSession(r.Context(), hash(t))
	w.WriteHeader(204)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	var id, name, email, oab, uf, role string
	var labor, consent bool
	err := s.DB.QueryRowContext(r.Context(), `SELECT id::text,name,email,coalesce(oab_number,''),coalesce(oab_uf,''),role,labor_practice,llm_consent FROM users WHERE id=$1`, user(r).ID).Scan(&id, &name, &email, &oab, &uf, &role, &labor, &consent)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	write(w, 200, map[string]any{"id": id, "name": name, "email": email, "oabNumber": oab, "oabUf": uf, "role": role, "laborPractice": labor, "llmConsent": consent})
}
func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Name          *string `json:"name"`
		LaborPractice *bool   `json:"laborPractice"`
		LLMConsent    *bool   `json:"llmConsent"`
	}
	if decode(r, &b) != nil {
		fail(w, 400, "invalid JSON")
		return
	}
	if b.Name != nil && strings.TrimSpace(*b.Name) == "" {
		fail(w, 400, "name required")
		return
	}
	if b.LLMConsent != nil && !*b.LLMConsent {
		fail(w, 400, "AI processing consent is required to use the platform")
		return
	}
	_, err := s.DB.ExecContext(r.Context(), `UPDATE users SET name=coalesce($2,name),labor_practice=coalesce($3,labor_practice),llm_consent=coalesce($4,llm_consent),llm_consented_at=CASE WHEN $4::boolean=TRUE AND llm_consented_at IS NULL THEN now() ELSE llm_consented_at END,updated_at=now() WHERE id=$1`, user(r).ID, b.Name, b.LaborPractice, b.LLMConsent)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	s.me(w, r)
}
func (s *Server) deleteMe(w http.ResponseWriter, r *http.Request) {
	_, err := s.DB.ExecContext(r.Context(), `DELETE FROM users WHERE id=$1`, user(r).ID)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	w.WriteHeader(204)
}

func sendReset(email, token string) error {
	addr := os.Getenv("SMTP_ADDR")
	from := os.Getenv("SMTP_FROM")
	if addr == "" || from == "" {
		return errors.New("SMTP not configured")
	}
	var auth smtp.Auth
	if u := os.Getenv("SMTP_USER"); u != "" {
		host := strings.Split(addr, ":")[0]
		auth = smtp.PlainAuth("", u, os.Getenv("SMTP_PASSWORD"), host)
	}
	instructions := fmt.Sprintf("Use POST /v1/auth/password-reset/confirm with token %s within 30 minutes.", token)
	resetURL, err := frontendURL("/reset-password", url.Values{"token": {token}})
	if err != nil {
		return err
	}
	if resetURL != "" {
		instructions = "Redefina sua senha neste link (válido por 30 minutos):\r\n" + resetURL
	}
	message := fmt.Sprintf("To: %s\r\nSubject: Redefinição de senha PerlinAdv\r\n\r\n%s\r\n", email, instructions)
	return smtp.SendMail(addr, auth, from, []string{email}, []byte(message))
}
func (s *Server) requestReset(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Email string `json:"email"`
	}
	if decode(r, &b) != nil {
		fail(w, 400, "invalid JSON")
		return
	}
	var id string
	err := s.DB.QueryRowContext(r.Context(), `SELECT id::text FROM users WHERE email=$1 AND active`, strings.ToLower(strings.TrimSpace(b.Email))).Scan(&id)
	if err == nil {
		t, e := token()
		if e == nil {
			_, e = s.DB.ExecContext(r.Context(), `INSERT INTO password_resets(user_id,token_hash,expires_at) VALUES($1,$2,now()+interval '30 minutes')`, id, hash(t))
			if e == nil {
				_ = sendReset(b.Email, t)
			}
		}
	}
	write(w, 202, map[string]string{"message": "If the account exists, reset instructions were sent"})
}
func (s *Server) confirmReset(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if decode(r, &b) != nil || len(b.Password) < 12 {
		fail(w, 400, "valid token and password (12+ characters) required")
		return
	}
	ph, err := bcrypt.GenerateFromPassword([]byte(b.Password), bcrypt.DefaultCost)
	if err != nil {
		fail(w, 500, "password error")
		return
	}
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(r.Context(), `UPDATE password_resets SET used_at=now() WHERE token_hash=$1 AND used_at IS NULL AND expires_at>now() RETURNING user_id::text`, hash(b.Token)).Scan(&id)
	if err == sql.ErrNoRows {
		fail(w, 400, "invalid or expired token")
		return
	}
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE users SET password_hash=$2,updated_at=now() WHERE id=$1`, id, string(ph)); err != nil {
		fail(w, 500, "database error")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id); err != nil {
		fail(w, 500, "database error")
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 500, "database error")
		return
	}
	write(w, 200, map[string]string{"message": "password updated"})
}

func (s *Server) listLawyers(w http.ResponseWriter, r *http.Request) {
	p, l := page(r)
	rows, err := s.DB.QueryContext(r.Context(), `SELECT id::text,name,email,coalesce(oab_number,''),coalesce(oab_uf,''),active,labor_practice FROM users WHERE role='LAWYER' ORDER BY created_at DESC LIMIT $1 OFFSET $2`, l, (p-1)*l)
	if err != nil {
		fail(w, 500, "database error")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, name, email, oab, uf string
		var active, labor bool
		if rows.Scan(&id, &name, &email, &oab, &uf, &active, &labor) == nil {
			items = append(items, map[string]any{"id": id, "name": name, "email": email, "oabNumber": oab, "oabUf": uf, "active": active, "laborPractice": labor})
		}
	}
	write(w, 200, map[string]any{"items": items, "page": p})
}
func (s *Server) updateLawyer(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Name          *string `json:"name"`
		Active        *bool   `json:"active"`
		LaborPractice *bool   `json:"laborPractice"`
	}
	if decode(r, &b) != nil {
		fail(w, 400, "invalid JSON")
		return
	}
	res, err := s.DB.ExecContext(r.Context(), `UPDATE users SET name=coalesce($2,name),active=coalesce($3,active),labor_practice=coalesce($4,labor_practice),updated_at=now() WHERE id=$1 AND role='LAWYER'`, param(r, "id"), b.Name, b.Active, b.LaborPractice)
	if err != nil {
		fail(w, 400, "invalid lawyer")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		fail(w, 404, "lawyer not found")
		return
	}
	w.WriteHeader(204)
}

// Keep json imported so future nullable response shapes remain straightforward.
var _ = json.Valid
var _ = time.Now
