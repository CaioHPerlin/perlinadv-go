package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/caiohperlin/perlinadv/internal/calendar"
)

func main() {
	year := flag.Int("year", time.Now().Year(), "TRT24 calendar year")
	out := flag.String("out", "private/calendar-drafts", "local drafts directory")
	input := flag.String("input", "", "previously downloaded official TRT24 HTML; default fetches the page")
	flag.Parse()
	clean := filepath.Clean(*out)
	if !strings.HasPrefix(clean, "private/") {
		log.Fatal("output must be under private/")
	}
	var source io.Reader
	if *input != "" {
		f, err := os.Open(*input)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		source = f
	} else {
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Get(calendar.TRT24CalendarURL)
		if err != nil {
			log.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			log.Fatalf("TRT24 HTTP %d", resp.StatusCode)
		}
		source = resp.Body
	}
	content, err := io.ReadAll(io.LimitReader(source, 5<<20))
	if err != nil || len(content) >= 5<<20 {
		log.Fatal("TRT24 page unreadable or too large")
	}
	drafts, err := calendar.PrepareTRT24(bytes.NewReader(content), *year)
	if err != nil {
		log.Fatal(err)
	}
	if err = os.MkdirAll(clean, 0700); err != nil {
		log.Fatal(err)
	}
	h := sha256.Sum256(content)
	if err = os.WriteFile(filepath.Join(clean, "trt24-source.sha256"), []byte(hex.EncodeToString(h[:])+"  "+calendar.TRT24CalendarURL+"\n"), 0600); err != nil {
		log.Fatal(err)
	}
	sort.Slice(drafts, func(i, j int) bool {
		return drafts[i].ScopeType+drafts[i].ScopeKey < drafts[j].ScopeType+drafts[j].ScopeKey
	})
	for _, draft := range drafts {
		name := fmt.Sprintf("TRT24_%s_%s_%d.json", draft.ScopeType, safeName(draft.ScopeKey), *year)
		data, err := json.MarshalIndent(draft, "", "  ")
		if err != nil {
			log.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(clean, name), append(data, '\n'), 0600); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Printf("prepared %d TRT24 drafts for %d under %s; compare with official acts before approval\n", len(drafts), *year, clean)
}
func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if b.Len() > 0 && !strings.HasSuffix(b.String(), "_") {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}
