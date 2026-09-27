package worker

import (
	"encoding/json"
	"strings"
	"testing"
)

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
