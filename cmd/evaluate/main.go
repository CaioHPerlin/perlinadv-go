package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/caiohperlin/perlinadv/internal/extract"
	"log"
	"os"
	"strings"
)

type example struct {
	Text string   `json:"text"`
	Gold []string `json:"gold"`
}

func main() {
	input := flag.String("input", "private/corpus.jsonl", "labeled JSONL corpus")
	flag.Parse()
	f, err := os.Open(*input)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 4<<20)
	tp, fp, fn, records := 0, 0, 0, 0
	for scan.Scan() {
		var ex example
		if err = json.Unmarshal(scan.Bytes(), &ex); err != nil {
			log.Fatal(err)
		}
		gold := map[string]bool{}
		for _, s := range ex.Gold {
			gold[strings.ToLower(strings.TrimSpace(s))] = true
		}
		seen := map[string]bool{}
		for _, c := range extract.Rules(ex.Text) {
			key := strings.ToLower(strings.TrimSpace(c.Evidence))
			seen[key] = true
			if gold[key] {
				tp++
			} else {
				fp++
			}
		}
		for key := range gold {
			if !seen[key] {
				fn++
			}
		}
		records++
	}
	if err = scan.Err(); err != nil {
		log.Fatal(err)
	}
	precision, recall := "n/a", "n/a"
	if tp+fp > 0 {
		precision = fmt.Sprintf("%.3f", float64(tp)/float64(tp+fp))
	}
	if tp+fn > 0 {
		recall = fmt.Sprintf("%.3f", float64(tp)/float64(tp+fn))
	}
	fmt.Printf("records=%d TP=%d FP=%d FN=%d precision=%s recall=%s\n", records, tp, fp, fn, precision, recall)
}
