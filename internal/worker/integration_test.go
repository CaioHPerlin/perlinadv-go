package worker

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/caiohperlin/perlinadv/internal/extract"
	_ "github.com/jackc/pgx/v5/stdlib"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type failedProvider struct{}

func (failedProvider) Extract(context.Context, string) ([]extract.Candidate, error) {
	return nil, errors.New("provider quota exhausted")
}

func TestExternalFailurePreservesPublicationIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	suffix := fmt.Sprint(time.Now().UnixNano())
	var userID, publicationID string
	if err = db.QueryRow(`INSERT INTO users(name,email,password_hash,llm_consent) VALUES('Test',$1,'unused',TRUE) RETURNING id::text`, "llm-"+suffix+"@example.invalid").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM users WHERE id=$1`, userID)
	source := "TEST:LLM:" + suffix
	defer db.Exec(`DELETE FROM publications WHERE source_id=$1`, source)
	if err = db.QueryRow(`INSERT INTO publications(source_id,source_hash,tribunal,content_text,available_on) VALUES($1,'hash','TJMS','Intimação para ciência da decisão.','2026-09-04') RETURNING id::text`, source).Scan(&publicationID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO user_publications(user_id,publication_id) VALUES($1,$2)`, userID, publicationID); err != nil {
		t.Fatal(err)
	}
	worker := Worker{DB: db, LLM: failedProvider{}}
	for range 3 {
		if err = worker.analyze(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	var status string
	var llmAssisted bool
	var count int
	if err = db.QueryRow(`SELECT analysis_status,llm_assisted FROM user_publications WHERE user_id=$1 AND publication_id=$2`, userID, publicationID).Scan(&status, &llmAssisted); err != nil {
		t.Fatal(err)
	}
	_ = db.QueryRow(`SELECT count(*) FROM publications WHERE id=$1`, publicationID).Scan(&count)
	if status != "FAILED" || count != 1 || !llmAssisted {
		t.Fatalf("analysis=%s publication_count=%d llm_assisted=%v", status, count, llmAssisted)
	}
}

func TestEditionToCalculatedSuggestionIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	suffix := fmt.Sprint(time.Now().UnixNano())
	email := "worker-" + suffix + "@example.invalid"
	oab := suffix
	locality := "TEST" + suffix
	sourceID := fmt.Sprint(time.Now().UnixNano() % 1000000000)
	var userID, caseID string
	err = db.QueryRow(`INSERT INTO users(name,email,password_hash,oab_number,oab_uf,llm_consent) VALUES('Test',$1,'unused',$2,'MS',TRUE) RETURNING id::text`, email, oab).Scan(&userID)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM users WHERE id=$1`, userID)
	defer db.Exec(`DELETE FROM publications WHERE source_id=$1`, "DJEN:"+sourceID)
	_, err = db.Exec(`UPDATE users SET monitoring_started_at='2026-09-05' WHERE id=$1`, userID)
	if err != nil {
		t.Fatal(err)
	}
	caseNumber := "00000000000000000001"
	err = db.QueryRow(`INSERT INTO cases(user_id,case_number,title,tribunal,locality) VALUES($1,$2,'Test','TJMS',$3) RETURNING id::text`, userID, caseNumber, locality).Scan(&caseID)
	if err != nil {
		t.Fatal(err)
	}
	var tribunalVersion, localVersion string
	for _, spec := range []struct {
		scope, key string
		dest       *string
	}{{"TRIBUNAL", "TJMS", &tribunalVersion}, {"COMARCA", locality, &localVersion}} {
		err = db.QueryRow(`INSERT INTO court_calendar_versions(tribunal,scope_type,scope_key,coverage_start,coverage_end,source_title,source_url,status,coverage_verified,approved_at) VALUES('TJMS',$1,$2,'2026-09-04','2026-09-20','Official test','https://tjms.jus.br/','ACTIVE',TRUE,now()) RETURNING id::text`, spec.scope, spec.key).Scan(spec.dest)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Exec(`DELETE FROM court_calendar_versions WHERE id=$1`, *spec.dest)
	}
	_, err = db.Exec(`INSERT INTO court_calendar_days(version_id,day,effect) VALUES($1,'2026-09-07','CLOSED')`, tribunalVersion)
	if err != nil {
		t.Fatal(err)
	}
	item := map[string]any{"id": json.Number(sourceID), "hash": "source-hash", "texto": "Intime-se para apresentar manifestação no prazo de 3 dias úteis.", "data_disponibilizacao": "2026-09-04", "numero_processo": caseNumber, "destinatarioadvogados": []any{map[string]any{"advogado": map[string]any{"numero_oab": oab, "uf_oab": "MS"}}}}
	var zipData bytes.Buffer
	zw := zip.NewWriter(&zipData)
	entry, err := zw.Create("caderno.json")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal([]any{item})
	_, _ = entry.Write(raw)
	_ = zw.Close()
	digest := sha256.Sum256(zipData.Bytes())
	var base string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/caderno/") {
			_ = json.NewEncoder(w).Encode(map[string]any{"url": base + "/edition.zip", "hash": hex.EncodeToString(digest[:]), "versao": "test"})
			return
		}
		if r.URL.Path == "/edition.zip" {
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(zipData.Bytes())
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()
	base = server.URL
	worker := Worker{DB: db, HTTP: server.Client(), BaseURL: base}
	if err = worker.ingestEdition(ctx, "TJMS", "2026-09-04"); err != nil {
		t.Fatal(err)
	}
	if err = worker.analyze(ctx); err != nil {
		t.Fatal(err)
	}
	var due, pub, first string
	err = db.QueryRow(`SELECT suggested_due_on::text,publication_on::text,first_counted_on::text FROM deadline_suggestions WHERE user_id=$1`, userID).Scan(&due, &pub, &first)
	if err != nil {
		t.Fatal(err)
	}
	if pub != "2026-09-08" || first != "2026-09-09" || due != "2026-09-11" {
		t.Fatalf("dates: publication=%s first=%s due=%s", pub, first, due)
	}
	if err = worker.ingestEdition(ctx, "TJMS", "2026-09-04"); err != nil {
		t.Fatal(err)
	}
	var count int
	_ = db.QueryRow(`SELECT count(*) FROM publications WHERE source_id=$1`, "DJEN:"+sourceID).Scan(&count)
	if count != 1 {
		t.Fatalf("replay created %d canonical publications", count)
	}
	var pubID string
	_ = db.QueryRow(`SELECT id::text FROM publications WHERE source_id=$1`, "DJEN:"+sourceID).Scan(&pubID)
	_, err = db.Exec(`INSERT INTO deadlines(user_id,publication_id,title,due_on) VALUES($1,$2,'Confirmed','2026-09-11')`, userID, pubID)
	if err != nil {
		t.Fatal(err)
	}
	item["hash"] = "revised-source-hash"
	zipData.Reset()
	zw = zip.NewWriter(&zipData)
	entry, err = zw.Create("caderno.json")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal([]any{item})
	_, _ = entry.Write(raw)
	_ = zw.Close()
	digest = sha256.Sum256(zipData.Bytes())
	if err = worker.ingestEdition(ctx, "TJMS", "2026-09-04"); err != nil {
		t.Fatal(err)
	}
	var review bool
	_ = db.QueryRow(`SELECT needs_review FROM deadlines WHERE user_id=$1`, userID).Scan(&review)
	if !review {
		t.Fatal("source revision did not flag confirmed deadline for review")
	}
	var status string
	_ = db.QueryRow(`SELECT status FROM djen_editions WHERE tribunal='TJMS' AND edition_on='2026-09-04' AND source_version='test'`).Scan(&status)
	if status != "DONE" {
		t.Fatalf("edition status %s", status)
	}
	_, _ = db.Exec(`DELETE FROM djen_editions WHERE tribunal='TJMS' AND edition_on='2026-09-04' AND source_version='test'`)
}
