package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestOwnedCRUDIntegration(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set")
	}
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	suffix := fmt.Sprint(time.Now().UnixNano())
	email1 := "test-" + suffix + "-1@example.invalid"
	email2 := "test-" + suffix + "-2@example.invalid"
	defer s.DB.Exec(`DELETE FROM users WHERE email IN ($1,$2)`, email1, email2)
	call := func(method, path, token string, body any) (int, map[string]any) {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		s.Router().ServeHTTP(w, req)
		var data map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &data)
		return w.Code, data
	}
	code, _ := call("POST", "/v1/auth/register", "", map[string]any{"name": "Tester", "email": email1, "password": "twelve-characters-123", "oabNumber": suffix + "x", "oabUf": "MS"})
	if code != 400 {
		t.Fatalf("registration without consent: %d", code)
	}
	for i, email := range []string{email1, email2} {
		code, data := call("POST", "/v1/auth/register", "", map[string]any{"name": "Tester", "email": email, "password": "twelve-characters-123", "oabNumber": fmt.Sprintf("%s%d", suffix, i), "oabUf": "MS", "llmConsent": true})
		if code != 201 {
			t.Fatalf("register %d: %d %v", i, code, data)
		}
	}
	code, login1 := call("POST", "/v1/auth/login", "", map[string]string{"email": email1, "password": "twelve-characters-123"})
	if code != 200 {
		t.Fatalf("login1: %d %v", code, login1)
	}
	token1 := login1["token"].(string)
	code, login2 := call("POST", "/v1/auth/login", "", map[string]string{"email": email2, "password": "twelve-characters-123"})
	if code != 200 {
		t.Fatalf("login2: %d %v", code, login2)
	}
	token2 := login2["token"].(string)
	if _, err = s.DB.Exec(`UPDATE users SET llm_consent=FALSE,llm_consented_at=NULL WHERE email=$1`, email2); err != nil {
		t.Fatal(err)
	}
	code, denied := call("GET", "/v1/dashboard", token2, nil)
	if code != 403 || denied["consentRequired"] != true {
		t.Fatalf("legacy account consent gate: %d %v", code, denied)
	}
	code, _ = call("PATCH", "/v1/me", token2, map[string]any{"llmConsent": true})
	if code != 200 {
		t.Fatalf("legacy account consent update: %d", code)
	}
	code, created := call("POST", "/v1/cases", token1, map[string]string{"caseNumber": "00000000000000000001", "title": "Processo de teste", "tribunal": "TJMS", "locality": "NOVA ANDRADINA"})
	if code != 200 {
		t.Fatalf("create case: %d %v", code, created)
	}
	id := created["id"].(string)
	code, dashboard := call("GET", "/v1/dashboard", token1, nil)
	if code != 200 || dashboard["activeCases"] != float64(1) {
		t.Fatalf("dashboard active cases: %d %v", code, dashboard)
	}
	code, _ = call("GET", "/v1/cases/"+id, token2, nil)
	if code != 404 {
		t.Fatalf("cross-user case access: %d", code)
	}
	code, deadline := call("POST", "/v1/deadlines", token1, map[string]string{"caseId": id, "title": "Prazo confirmado", "dueOn": "2026-10-01"})
	if code != 200 {
		t.Fatalf("create deadline: %d %v", code, deadline)
	}
	code, _ = call("GET", "/v1/deadlines/"+deadline["id"].(string), token2, nil)
	if code != 404 {
		t.Fatalf("cross-user deadline access: %d", code)
	}
	var uid, publicationID, suggestionID string
	if err = s.DB.QueryRow(`SELECT id::text FROM users WHERE email=$1`, email1).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	source := "TEST:" + suffix
	defer s.DB.Exec(`DELETE FROM publications WHERE source_id=$1`, source)
	if err = s.DB.QueryRow(`INSERT INTO publications(source_id,source_hash,tribunal,content_text,available_on) VALUES($1,'hash','TJMS','Intime-se para apresentar em 3 dias úteis','2026-09-04') RETURNING id::text`, source).Scan(&publicationID); err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.Exec(`INSERT INTO user_publications(user_id,publication_id) VALUES($1,$2)`, uid, publicationID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow(`INSERT INTO deadline_suggestions(user_id,publication_id,action,evidence,duration_days,unit,method,rule_version,suggested_due_on) VALUES($1,$2,'apresentar','3 dias úteis',3,'BUSINESS_DAYS','RULE','test','2026-09-11') RETURNING id::text`, uid, publicationID).Scan(&suggestionID); err != nil {
		t.Fatal(err)
	}
	code, publication := call("GET", "/v1/publications/"+publicationID, token1, nil)
	if code != 200 || publication["llmAssisted"] != false {
		t.Fatalf("rule publication provenance: %d %v", code, publication)
	}
	code, suggestions := call("GET", "/v1/suggestions", token1, nil)
	if code != 200 || len(suggestions["items"].([]any)) == 0 || suggestions["items"].([]any)[0].(map[string]any)["llmAssisted"] != false {
		t.Fatalf("rule suggestion provenance: %d %v", code, suggestions)
	}
	_, err = s.DB.Exec(`INSERT INTO google_connections(user_id,refresh_token_cipher,calendar_id) VALUES($1,$2,'primary')`, uid, []byte{1})
	if err != nil {
		t.Fatal(err)
	}
	var jobs int
	_ = s.DB.QueryRow(`SELECT count(*) FROM google_jobs WHERE user_id=$1`, uid).Scan(&jobs)
	if jobs != 0 {
		t.Fatalf("unconfirmed suggestion created %d Google jobs", jobs)
	}
	code, _ = call("POST", "/v1/suggestions/"+suggestionID+"/confirm", token2, map[string]string{"title": "Outro", "dueOn": "2026-09-11"})
	if code != 404 {
		t.Fatalf("cross-user confirmation: %d", code)
	}
	code, confirmed := call("POST", "/v1/suggestions/"+suggestionID+"/confirm", token1, map[string]string{"title": "Revisado", "dueOn": "2026-09-12"})
	if code != 200 || confirmed["llmAssisted"] != false {
		t.Fatalf("confirmation provenance: %d %v", code, confirmed)
	}
	_ = s.DB.QueryRow(`SELECT count(*) FROM google_jobs WHERE user_id=$1`, uid).Scan(&jobs)
	if jobs != 1 {
		t.Fatalf("confirmed suggestion created %d Google jobs", jobs)
	}
	code, _ = call("POST", "/v1/auth/logout", token1, nil)
	if code != 204 {
		t.Fatalf("logout: %d", code)
	}
	code, _ = call("GET", "/v1/me", token1, nil)
	if code != 401 {
		t.Fatalf("revoked session: %d", code)
	}
}

var _ = http.MethodGet
