package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOABRangeSplitsAtDJENLimit(t *testing.T) {
	zone, _ := time.LoadLocation("America/Campo_Grande")
	start := time.Date(2026, 10, 11, 12, 0, 0, 0, zone)
	end := start.AddDate(0, 0, 1)
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(out http.ResponseWriter, request *http.Request) {
		from := request.URL.Query().Get("dataDisponibilizacaoInicio")
		to := request.URL.Query().Get("dataDisponibilizacaoFim")
		requests = append(requests, from+".."+to)
		count := 0
		if from != to {
			count = 10000
		}
		_ = json.NewEncoder(out).Encode(map[string]any{"count": count, "items": []any{}})
	}))
	defer server.Close()
	worker := Worker{HTTP: server.Client(), BaseURL: server.URL}
	if _, err := worker.pollLawyerRange(context.Background(), lawyerPoll{number: "7242", uf: "MS"}, "TJMS", start, end); err != nil {
		t.Fatal(err)
	}
	want := []string{"2026-10-11..2026-10-12", "2026-10-11..2026-10-11", "2026-10-12..2026-10-12"}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("range requests = %v, want %v", requests, want)
	}
}

func TestOnboardingDaysAndDailyCatchup(t *testing.T) {
	zone, _ := time.LoadLocation("America/Campo_Grande")
	monday := time.Date(2026, 10, 12, 12, 0, 0, 0, zone)
	if got, want := onboardingDays(monday), []string{"2026-10-09", "2026-10-11", "2026-10-12"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Monday onboarding days = %v, want %v", got, want)
	}
	start, err := recurringStart(monday, "2026-10-12", sql.NullTime{}, sql.NullTime{}, sql.NullTime{})
	if err != nil || start.Format("2006-01-02") != "2026-10-09" {
		t.Fatalf("first daily collection starts on %s: %v", start, err)
	}
	covered := sql.NullTime{Time: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), Valid: true}
	start, err = recurringStart(monday, "2026-09-01", covered, sql.NullTime{}, sql.NullTime{})
	if err != nil || start.Format("2006-01-02") != "2026-10-01" {
		t.Fatalf("outage must resume after checkpoint: %s: %v", start, err)
	}
	covered = sql.NullTime{Time: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC), Valid: true}
	lastSuccess := sql.NullTime{Time: monday, Valid: true}
	start, err = recurringStart(monday, "2026-09-01", covered, lastSuccess, sql.NullTime{})
	if err != nil || start.Format("2006-01-02") != "2026-10-12" {
		t.Fatalf("hourly checks must revisit today: %s: %v", start, err)
	}
	retryFrom := sql.NullTime{Time: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC), Valid: true}
	start, err = recurringStart(monday, "2026-09-01", covered, lastSuccess, retryFrom)
	if err != nil || start.Format("2006-01-02") != "2026-10-06" {
		t.Fatalf("failed revision must resume its exact day: %s: %v", start, err)
	}
}

func TestConsumeJSONShapes(t *testing.T) {
	for _, input := range []string{`[{"id":1,"texto":"x"}]`, `{"items":[{"id":1,"texto":"x"}]}`, "{\"id\":1,\"texto\":\"x\"}\n"} {
		count := 0
		err := consumeJSON(strings.NewReader(input), func(item djenItem) error {
			count++
			if item.ID != json.Number("1") {
				t.Fatalf("id: %s", item.ID)
			}
			return nil
		})
		if err != nil || count != 1 {
			t.Fatalf("shape %s: count=%d err=%v", input, count, err)
		}
	}
}
func TestPickNestedManifest(t *testing.T) {
	obj := map[string]any{"items": []any{map[string]any{"urlArquivo": "https://example.org/x.zip"}}}
	if pick(obj, "urlArquivo") != "https://example.org/x.zip" {
		t.Fatal("nested URL not found")
	}
}
func TestGoogleEventIDStable(t *testing.T) {
	id := deterministicEventID("123")
	if id != deterministicEventID("123") || len(id) != 32 || id == deterministicEventID("124") {
		t.Fatalf("invalid deterministic ID: %s", id)
	}
}
