package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestFirstLoginSyncLifecycleIntegration(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set")
	}
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	suffix := fmt.Sprint(time.Now().UnixNano())
	email := "first-sync-" + suffix + "@example.invalid"
	defer s.DB.Exec(`DELETE FROM users WHERE email=$1`, email)
	call := func(method, path, token string, body any) (int, map[string]any) {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res := httptest.NewRecorder()
		s.Router().ServeHTTP(res, req)
		var data map[string]any
		_ = json.Unmarshal(res.Body.Bytes(), &data)
		return res.Code, data
	}
	code, registered := call("POST", "/v1/auth/register", "", map[string]any{"name": "Advogada", "email": email, "password": "twelve-characters-123", "oabNumber": suffix, "oabUf": "MS", "llmConsent": true})
	if code != 201 {
		t.Fatalf("register: %d %v", code, registered)
	}
	userID := registered["id"].(string)
	code, loggedIn := call("POST", "/v1/auth/login", "", map[string]string{"email": email, "password": "twelve-characters-123"})
	if code != 200 {
		t.Fatalf("login: %d %v", code, loggedIn)
	}
	token := loggedIn["token"].(string)
	code, state := call("GET", "/v1/onboarding/publications", token, nil)
	if code != 200 || state["status"] != "PENDING" || state["progress"] != float64(0) || state["acknowledged"] != false {
		t.Fatalf("initial state: %d %v", code, state)
	}
	if code, _ := call("POST", "/v1/onboarding/publications/acknowledge", token, nil); code != 409 {
		t.Fatalf("premature acknowledgement: %d", code)
	}
	if _, err := s.DB.Exec(`UPDATE lawyer_onboarding_syncs SET status='DONE',progress=100,publication_count=2 WHERE user_id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	code, state = call("GET", "/v1/onboarding/publications", token, nil)
	if code != 200 || state["status"] != "DONE" || state["publicationCount"] != float64(2) {
		t.Fatalf("completed state: %d %v", code, state)
	}
	if code, _ := call("POST", "/v1/onboarding/publications/acknowledge", token, nil); code != 204 {
		t.Fatalf("acknowledgement: %d", code)
	}
	_, state = call("GET", "/v1/onboarding/publications", token, nil)
	if state["acknowledged"] != true {
		t.Fatalf("acknowledgement not persisted: %v", state)
	}
	if code, _ := call("POST", "/v1/onboarding/publications/retry", token, nil); code != 409 {
		t.Fatalf("retry of completed sync: %d", code)
	}
	if _, err := s.DB.Exec(`UPDATE lawyer_onboarding_syncs SET status='FAILED' WHERE user_id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	if code, _ := call("POST", "/v1/onboarding/publications/retry", token, nil); code != 202 {
		t.Fatalf("retry failed sync: %d", code)
	}
	_, state = call("GET", "/v1/onboarding/publications", token, nil)
	if state["status"] != "PENDING" || state["acknowledged"] != false {
		t.Fatalf("retry state: %v", state)
	}
}
