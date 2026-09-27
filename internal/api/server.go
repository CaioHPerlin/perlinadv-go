package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/caiohperlin/perlinadv/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type Server struct {
	DB     *sql.DB
	router *chi.Mux
	HTTP   *http.Client
}
type principal struct{ ID, Role string }
type contextKey string

const principalKey contextKey = "principal"

func New() (*Server, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(15)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	s := &Server{DB: db, router: chi.NewRouter(), HTTP: &http.Client{Timeout: 30 * time.Second}}
	s.routes()
	return s, nil
}
func (s *Server) Close() error         { return s.DB.Close() }
func (s *Server) Router() http.Handler { return s.router }

func (s *Server) routes() {
	s.router.Use(middleware.RequestID, middleware.RealIP, middleware.Logger, middleware.Recoverer)
	s.router.Get("/health", func(w http.ResponseWriter, r *http.Request) { write(w, 200, map[string]string{"status": "ok"}) })
	s.router.Route("/v1", func(r chi.Router) {
		r.Post("/auth/register", s.register)
		r.Post("/auth/login", s.login)
		r.Post("/auth/password-reset/request", s.requestReset)
		r.Post("/auth/password-reset/confirm", s.confirmReset)
		r.Get("/google/callback", s.googleCallback)
		r.Group(func(r chi.Router) {
			r.Use(s.auth)
			r.Post("/auth/logout", s.logout)
			r.Get("/me", s.me)
			r.Patch("/me", s.updateMe)
			r.Delete("/me", s.deleteMe)
			r.Get("/cases", s.listCases)
			r.Post("/cases", s.createCase)
			r.Get("/cases/{id}", s.getCase)
			r.Put("/cases/{id}", s.updateCase)
			r.Delete("/cases/{id}", s.deleteCase)
			r.Get("/publications", s.listPublications)
			r.Get("/publications/{id}", s.getPublication)
			r.Patch("/publications/{id}/read", s.setPublicationRead)
			r.Post("/publications/{id}/cases/{caseID}", s.linkPublication)
			r.Delete("/publications/{id}/cases/{caseID}", s.unlinkPublication)
			r.Get("/suggestions", s.listSuggestions)
			r.Post("/suggestions/{id}/confirm", s.confirmSuggestion)
			r.Post("/suggestions/{id}/discard", s.discardSuggestion)
			r.Get("/deadlines", s.listDeadlines)
			r.Post("/deadlines", s.createDeadline)
			r.Get("/deadlines/{id}", s.getDeadline)
			r.Put("/deadlines/{id}", s.updateDeadline)
			r.Delete("/deadlines/{id}", s.deleteDeadline)
			r.Get("/dashboard", s.dashboard)
			r.Get("/google/status", s.googleStatus)
			r.Get("/google/connect", s.googleConnect)
			r.Get("/google/calendars", s.googleCalendars)
			r.Put("/google/calendar", s.googleSelectCalendar)
			r.Delete("/google/connection", s.googleDisconnect)
			r.Group(func(r chi.Router) {
				r.Use(s.admin)
				r.Get("/admin/lawyers", s.listLawyers)
				r.Patch("/admin/lawyers/{id}", s.updateLawyer)
				r.Get("/admin/calendars", s.listCalendars)
				r.Post("/admin/calendars/import", s.importCalendar)
				r.Post("/admin/calendars/{id}/approve", s.approveCalendar)
				r.Get("/admin/sync", s.syncStatus)
			})
		})
	})
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, msg string) {
	write(w, status, map[string]string{"error": msg})
}
func decode(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("multiple JSON values")
	}
	return nil
}
func param(r *http.Request, key string) string { return chi.URLParam(r, key) }
func withParam(r *http.Request, key, value string) *http.Request {
	chi.RouteContext(r.Context()).URLParams.Add(key, value)
	return r
}
func user(r *http.Request) principal { return r.Context().Value(principalKey).(principal) }
func page(r *http.Request) (int, int) {
	p, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if p < 1 {
		p = 1
	}
	l, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if l < 1 {
		l = 25
	}
	if l > 100 {
		l = 100
	}
	return p, l
}
func token() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func hash(t string) []byte { h := sha256.Sum256([]byte(t)); return h[:] }
func normOAB(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' {
			return r
		}
		return -1
	}, s)
}
func validDate(s string) bool { _, err := time.Parse("2006-01-02", s); return err == nil }
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			fail(w, 401, "bearer token required")
			return
		}
		account, err := store.New(s.DB).GetSessionPrincipal(r.Context(), hash(parts[1]))
		if err != nil {
			fail(w, 401, "invalid session")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, principal{ID: account.ID.String(), Role: account.Role})))
	})
}
func (s *Server) admin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user(r).Role != "ADMIN" {
			fail(w, 403, "admin required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
