package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestFirstLoginSyncFetchesTodaysPublicationsIntegration(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	suffix := fmt.Sprint(time.Now().UnixNano())
	var userID string
	if err := db.QueryRow(`INSERT INTO users(name,email,password_hash,oab_number,oab_uf,llm_consent) VALUES('Onboarding',$1,'unused',$2,'MS',TRUE) RETURNING id::text`, "onboarding-"+suffix+"@example.invalid", suffix).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM users WHERE id=$1`, userID)
	if _, err := db.Exec(`INSERT INTO lawyer_onboarding_syncs(user_id) VALUES($1)`, userID); err != nil {
		t.Fatal(err)
	}
	sourceID := strconv.FormatInt(time.Now().UnixNano()%900000000+100000000, 10)
	defer db.Exec(`DELETE FROM publications WHERE source_id=$1`, "DJEN:"+sourceID)
	zone, _ := time.LoadLocation("America/Campo_Grande")
	today := time.Now().In(zone).Format("2006-01-02")
	days := onboardingDays(time.Now().In(zone))
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/comunicacao" || q.Get("numeroOab") != suffix || q.Get("ufOab") != "MS" || q.Get("dataDisponibilizacaoInicio") != days[0] || q.Get("dataDisponibilizacaoFim") != today || q.Get("meio") != "D" || q.Get("itensPorPagina") != "100" {
			t.Errorf("unexpected DJEN request: %s", r.URL.String())
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		tribunal := q.Get("siglaTribunal")
		requests = append(requests, tribunal)
		if tribunal == "TRF3" {
			var progress int
			if err := db.QueryRow(`SELECT progress FROM lawyer_onboarding_syncs WHERE user_id=$1`, userID).Scan(&progress); err != nil || progress < 50 {
				t.Errorf("progress before second tribunal = %d, err = %v", progress, err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"count": 0, "items": []any{}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"count": 1, "items": []any{map[string]any{
			"id": json.Number(sourceID), "hash": "onboarding-test", "siglaTribunal": "TJMS", "texto": "Publicação inicial.",
			"data_disponibilizacao": today, "destinatarioadvogados": []any{map[string]any{"advogado": map[string]any{"numero_oab": suffix, "uf_oab": "MS"}}},
		}}})
	}))
	defer server.Close()
	worker := Worker{DB: db, HTTP: server.Client(), BaseURL: server.URL}
	if err := worker.processOnboardingSync(context.Background(), userID); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || requests[0] != "TJMS" || requests[1] != "TRF3" {
		t.Fatalf("unexpected tribunals: %v", requests)
	}
	var status string
	var progress, count, linked int
	if err := db.QueryRow(`SELECT status,progress,publication_count FROM lawyer_onboarding_syncs WHERE user_id=$1`, userID).Scan(&status, &progress, &count); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM user_publications WHERE user_id=$1`, userID).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if status != "DONE" || progress != 100 || count != 1 || linked != 1 {
		t.Fatalf("sync status=%s progress=%d count=%d linked=%d", status, progress, count, linked)
	}
}

func TestFirstLoginSyncReportsDJENFailureIntegration(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	suffix := fmt.Sprint(time.Now().UnixNano())
	var userID string
	if err := db.QueryRow(`INSERT INTO users(name,email,password_hash,oab_number,oab_uf,llm_consent) VALUES('Onboarding',$1,'unused',$2,'MS',TRUE) RETURNING id::text`, "onboarding-fail-"+suffix+"@example.invalid", suffix).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM users WHERE id=$1`, userID)
	if _, err := db.Exec(`INSERT INTO lawyer_onboarding_syncs(user_id) VALUES($1)`, userID); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	worker := Worker{DB: db, HTTP: server.Client(), BaseURL: server.URL}
	if err := worker.processOnboardingSync(context.Background(), userID); err == nil {
		t.Fatal("expected DJEN failure")
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM lawyer_onboarding_syncs WHERE user_id=$1`, userID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "FAILED" {
		t.Fatalf("expected FAILED, got %s", status)
	}
}
