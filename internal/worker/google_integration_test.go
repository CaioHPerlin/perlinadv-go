package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/caiohperlin/perlinadv/internal/google"
	_ "github.com/jackc/pgx/v5/stdlib"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGoogleOnlyConfirmedDeadlineIntegration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	email := fmt.Sprintf("google-%d@example.invalid", time.Now().UnixNano())
	var uid, did string
	err = db.QueryRow(`INSERT INTO users(name,email,password_hash) VALUES('Test',$1,'unused') RETURNING id::text`, email).Scan(&uid)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM users WHERE id=$1`, uid)
	err = db.QueryRow(`INSERT INTO deadlines(user_id,title,due_on) VALUES($1,'Petição','2026-10-01') RETURNING id::text`, uid).Scan(&did)
	if err != nil {
		t.Fatal(err)
	}
	key := []byte("12345678901234567890123456789012")
	calls := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/token" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"access"}`))
			return
		}
		if r.Method == "POST" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["summary"] != "Prazo: Petição" {
				t.Errorf("wrong event summary: %v", body)
			}
			w.WriteHeader(200)
			return
		}
		if r.Method == "DELETE" {
			w.WriteHeader(204)
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()
	c := google.Client{HTTP: server.Client(), ClientID: "test", ClientSecret: "test", Key: key, TokenURL: server.URL + "/token", APIBaseURL: server.URL}
	cipher, err := c.Encrypt("refresh")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO google_connections(user_id,refresh_token_cipher,calendar_id) VALUES($1,$2,'primary')`, uid, cipher)
	if err != nil {
		t.Fatal(err)
	}
	w := Worker{DB: db}
	j := googleJob{user: uid, deadline: did, operation: "UPSERT"}
	if err = w.processGoogle(context.Background(), c, j); err != nil {
		t.Fatal(err)
	}
	var eventID string
	err = db.QueryRow(`SELECT event_id FROM google_events WHERE deadline_id=$1`, did).Scan(&eventID)
	if err != nil {
		t.Fatal(err)
	}
	j = googleJob{user: uid, operation: "DELETE", eventID: eventID, calendarID: "primary"}
	if err = w.processGoogle(context.Background(), c, j); err != nil {
		t.Fatal(err)
	}
	var count int
	_ = db.QueryRow(`SELECT count(*) FROM google_events WHERE deadline_id=$1`, did).Scan(&count)
	if count != 0 {
		t.Fatalf("event mapping remains: %d", count)
	}
	if len(calls) != 4 || !strings.HasPrefix(calls[1], "POST ") || !strings.HasPrefix(calls[3], "DELETE ") {
		t.Fatalf("unexpected Google calls: %v", calls)
	}
}
