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

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestOABPollPaginatesAndDeduplicatesIntegration(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	suffix := fmt.Sprint(time.Now().UnixNano())
	lawyer := lawyerPoll{number: suffix, uf: "MS"}
	if err := db.QueryRow(`INSERT INTO users(name,email,password_hash,oab_number,oab_uf,llm_consent) VALUES('Test',$1,'unused',$2,'MS',TRUE) RETURNING id::text`, "poll-"+suffix+"@example.invalid", suffix).Scan(&lawyer.id); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM users WHERE id=$1`, lawyer.id)
	ids := []string{strconv.FormatInt(time.Now().UnixNano()%900000000+100000000, 10), strconv.FormatInt(time.Now().UnixNano()%900000000+100000001, 10)}
	for _, id := range ids {
		defer db.Exec(`DELETE FROM publications WHERE source_id=$1`, "DJEN:"+id)
	}
	day := time.Now().In(time.FixedZone("MS", -4*3600)).AddDate(0, 0, -1).Format("2006-01-02")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/comunicacao" || q.Get("numeroOab") != suffix || q.Get("ufOab") != "MS" || q.Get("siglaTribunal") != "TJMS" || q.Get("itensPorPagina") != "50" {
			t.Errorf("unexpected request: %s", r.URL.String())
			w.WriteHeader(400)
			return
		}
		page, _ := strconv.Atoi(q.Get("pagina"))
		if page < 1 || page > 2 {
			t.Errorf("unexpected page %d", page)
			w.WriteHeader(400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"count": 2, "items": []any{map[string]any{
			"id": json.Number(ids[page-1]), "hash": "hash", "siglaTribunal": "TJMS", "texto": "Intime-se para ciência.",
			"data_disponibilizacao": day, "destinatarioadvogados": []any{map[string]any{"advogado": map[string]any{"numero_oab": suffix, "uf_oab": "MS"}}},
		}}})
	}))
	defer server.Close()
	w := Worker{DB: db, HTTP: server.Client(), BaseURL: server.URL}
	for range 2 {
		count, err := w.pollLawyerTribunal(context.Background(), lawyer, "TJMS", day, day)
		if err != nil || count != 2 {
			t.Fatalf("poll count=%d err=%v", count, err)
		}
	}
	var linked int
	if err := db.QueryRow(`SELECT count(*) FROM user_publications WHERE user_id=$1 AND publication_id IN (SELECT id FROM publications WHERE source_id IN ($2,$3))`, lawyer.id, "DJEN:"+ids[0], "DJEN:"+ids[1]).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked != 2 {
		t.Fatalf("expected two unique lawyer publications, got %d", linked)
	}
}
