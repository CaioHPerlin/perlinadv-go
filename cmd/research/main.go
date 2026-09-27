// Research helper for a consented pilot. Output is local and must not be committed.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type item struct {
	ID        json.Number `json:"id"`
	Text      string      `json:"texto"`
	Available string      `json:"data_disponibilizacao"`
	Tribunal  string      `json:"siglaTribunal"`
}
type response struct {
	Count int    `json:"count"`
	Items []item `json:"items"`
}

func main() {
	oab := flag.String("oab", "", "consented OAB number")
	uf := flag.String("uf", "", "OAB state")
	out := flag.String("out", "private/corpus.jsonl", "local output, never commit")
	pages := flag.Int("pages", 20, "maximum pages")
	days := flag.Int("days", 90, "keep publications from this many recent days")
	flag.Parse()
	if *oab == "" || len(*uf) != 2 {
		log.Fatal("-oab and -uf required")
	}
	if *pages < 1 || *pages > 100 {
		log.Fatal("pages must be 1..100")
	}
	if *days < 1 || *days > 1095 {
		log.Fatal("days must be 1..1095")
	}
	if !strings.HasPrefix(filepath.Clean(*out), "private/") {
		log.Fatal("output must be under private/")
	}
	if err := os.MkdirAll("private", 0700); err != nil {
		log.Fatal(err)
	}
	f, err := os.OpenFile(*out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	base := os.Getenv("DJEN_BASE_URL")
	if base == "" {
		base = "https://comunicaapi.pje.jus.br/api/v1"
	}
	client := &http.Client{Timeout: 30 * time.Second}
	encoded := json.NewEncoder(f)
	total := 0
	cutoff := time.Now().AddDate(0, 0, -*days).Format("2006-01-02")
	for page := 1; page <= *pages; page++ {
		u, err := url.Parse(strings.TrimRight(base, "/") + "/comunicacao")
		if err != nil {
			log.Fatal(err)
		}
		q := u.Query()
		q.Set("numeroOab", *oab)
		q.Set("ufOab", strings.ToUpper(*uf))
		q.Set("itensPorPagina", "100")
		q.Set("pagina", strconv.Itoa(page))
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(context.Background(), "GET", u.String(), nil)
		if err != nil {
			log.Fatal(err)
		}
		res, err := client.Do(req)
		if err != nil {
			log.Fatal(err)
		}
		if res.StatusCode != 200 {
			res.Body.Close()
			log.Fatalf("DJEN HTTP %d", res.StatusCode)
		}
		var data response
		err = json.NewDecoder(res.Body).Decode(&data)
		remaining := res.Header.Get("x-ratelimit-remaining")
		res.Body.Close()
		if err != nil {
			log.Fatal(err)
		}
		for _, it := range data.Items {
			if len(it.Available) < 10 || it.Available[:10] < cutoff {
				continue
			}
			if err = encoded.Encode(map[string]any{"sourceId": it.ID.String(), "availableOn": it.Available, "tribunal": it.Tribunal, "text": it.Text, "gold": []string{}}); err != nil {
				log.Fatal(err)
			}
			total++
		}
		if len(data.Items) < 100 || remaining == "0" {
			break
		}
		time.Sleep(time.Second)
	}
	fmt.Printf("saved %d publications to %s; label gold evidence locally before evaluation\n", total, *out)
}
