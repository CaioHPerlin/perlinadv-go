package extract

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRulesRequireActionAndExplicitBusinessDays(t *testing.T) {
	text := "Fica a parte intimada para apresentar manifestação no prazo de 15 dias úteis."
	found := Rules(text)
	if len(found) != 1 || found[0].DurationDays != 15 || found[0].Evidence != "prazo de 15 dias úteis" {
		t.Fatalf("unexpected candidates: %+v", found)
	}
	if got := Rules("Prazo legal para manifestação."); len(got) != 0 {
		t.Fatalf("implicit deadline: %+v", got)
	}
	for _, example := range []struct {
		text string
		days int
	}{
		{"Intime-se a agravada para responder, querendo, no prazo de15(quinze) dias úteis.", 15},
		{"Intimem-se as partes para que, no prazo comum de dez dias úteis, apontem as questões.", 10},
	} {
		got := Rules(example.text)
		if len(got) != 1 || got[0].DurationDays != example.days {
			t.Fatalf("expected %d business days in %q, got %+v", example.days, example.text, got)
		}
	}
	if got := Rules("As sessões virtuais assíncronas terão duração de 3 dias úteis."); len(got) != 0 {
		t.Fatalf("nonprocedural duration: %+v", got)
	}
}
func TestExternalRedaction(t *testing.T) {
	s := cleanForExternal("CPF 123.456.789-00 email pessoa@example.com")
	if s != "CPF [CPF] email [EMAIL]" {
		t.Fatalf("redaction: %q", s)
	}
}
func TestCloudflareProviderValidatesEvidenceAndRedacts(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization header not passed")
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "123.456.789-00") || !strings.Contains(string(body), "[CPF]") {
			t.Errorf("CPF not redacted from prompt")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"result":{"response":"{\"items\":[{\"action\":\"impugnar\",\"evidence\":\"5 dias úteis\",\"durationDays\":5,\"unit\":\"BUSINESS_DAYS\"},{\"action\":\"inventado\",\"evidence\":\"10 dias úteis\",\"durationDays\":10,\"unit\":\"BUSINESS_DAYS\"}]}"}}`))}, nil
	})}
	p := CloudflareProvider{AccountID: "test", APIToken: "test-token", Model: "test-model", HTTP: client, Endpoint: "https://example.invalid/ai"}
	found, err := p.Extract(context.Background(), "Impugnação em 5 dias úteis. CPF 123.456.789-00")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Evidence != "5 dias úteis" || found[0].Method != "LLM" {
		t.Fatalf("unexpected validated candidates: %+v", found)
	}
}
