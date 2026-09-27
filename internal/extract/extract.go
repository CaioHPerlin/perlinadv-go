package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const RuleVersion = "rules-v2"

type Candidate struct {
	Action       string `json:"action"`
	Evidence     string `json:"evidence"`
	DurationDays int    `json:"durationDays"`
	Unit         string `json:"unit"`
	Method       string `json:"method"`
	RuleVersion  string `json:"ruleVersion"`
}

type Provider interface {
	Extract(context.Context, string) ([]Candidate, error)
}

type CloudflareProvider struct {
	AccountID string
	APIToken  string
	Model     string
	HTTP      *http.Client
	Endpoint  string
}

func CloudflareFromEnv() CloudflareProvider {
	return CloudflareProvider{AccountID: os.Getenv("CLOUDFLARE_ACCOUNT_ID"), APIToken: os.Getenv("CLOUDFLARE_API_TOKEN"), Model: os.Getenv("CLOUDFLARE_MODEL")}
}

var duration = regexp.MustCompile(`(?i)\bprazo\s+(?:comum\s+)?de\s*(\d{1,3}|um|uma|dois|duas|tr[eê]s|quatro|cinco|seis|sete|oito|nove|dez|quinze|vinte|trinta)(?:\s*\([a-záéíóúâêôãõç]+\))?\s*dias?\s+[uú]teis\b`)
var action = regexp.MustCompile(`(?i)\b(intimad[oa]s?|intime-se|intimem-se|intimam-se|manifeste-se|manifestar-se|apresent[ea]|apresentarem|interponha|recorr[ea]|emend[ea]|comprov[ea]|recolh[ea]|responder|ofertar|apontem)\b`)
var cpf = regexp.MustCompile(`\b\d{3}\.\d{3}\.\d{3}-\d{2}\b`)
var email = regexp.MustCompile(`\b[\w.+-]+@[\w.-]+\.[A-Za-z]{2,}\b`)

func Rules(text string) []Candidate {
	out := []Candidate{}
	for _, loc := range duration.FindAllStringSubmatchIndex(text, -1) {
		n, err := strconv.Atoi(text[loc[2]:loc[3]])
		if err != nil {
			n = wordDays(strings.ToLower(text[loc[2]:loc[3]]))
		}
		if n < 1 || n > 365 {
			continue
		}
		start := loc[0] - 140
		if start < 0 {
			start = 0
		}
		end := loc[1] + 80
		if end > len(text) {
			end = len(text)
		}
		window := text[start:end]
		a := action.FindString(window)
		if a == "" {
			continue
		}
		out = append(out, Candidate{Action: a, Evidence: text[loc[0]:loc[1]], DurationDays: n, Unit: "BUSINESS_DAYS", Method: "RULE", RuleVersion: RuleVersion})
	}
	return out
}
func wordDays(s string) int {
	switch s {
	case "um", "uma":
		return 1
	case "dois", "duas":
		return 2
	case "três", "tres":
		return 3
	case "quatro":
		return 4
	case "cinco":
		return 5
	case "seis":
		return 6
	case "sete":
		return 7
	case "oito":
		return 8
	case "nove":
		return 9
	case "dez":
		return 10
	case "quinze":
		return 15
	case "vinte":
		return 20
	case "trinta":
		return 30
	}
	return 0
}
func cleanForExternal(s string) string {
	if len(s) > 10000 {
		s = s[:10000]
	}
	s = cpf.ReplaceAllString(s, "[CPF]")
	return email.ReplaceAllString(s, "[EMAIL]")
}
func (p CloudflareProvider) Extract(ctx context.Context, text string) ([]Candidate, error) {
	if p.AccountID == "" || p.APIToken == "" {
		return nil, errors.New("Cloudflare not configured")
	}
	model := p.Model
	if model == "" {
		model = "@cf/meta/llama-3.3-70b-instruct-fp8-fast"
	}
	prompt := `Extract only explicitly stated procedural deadlines from the Portuguese judicial publication. Return JSON object {"items":[{"action":"...","evidence":"exact substring","durationDays":number,"unit":"BUSINESS_DAYS"}]} or {"items":[]}. Never invent a legal rule or date. Only include deadlines stated in business days. Publication: ` + cleanForExternal(text)
	body, _ := json.Marshal(map[string]any{"messages": []map[string]string{{"role": "user", "content": prompt}}, "max_tokens": 350, "temperature": 0})
	endpoint := p.Endpoint
	if endpoint == "" {
		endpoint = "https://api.cloudflare.com/client/v4/accounts/" + p.AccountID + "/ai/run/" + model
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.APIToken)
	req.Header.Set("Content-Type", "application/json")
	client := p.HTTP
	if client == nil {
		client = &http.Client{Timeout: 25 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("LLM request failed")
	}
	var envelope struct {
		Result struct {
			Response string `json:"response"`
		} `json:"result"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil {
		return nil, err
	}
	var parsed struct {
		Items []Candidate `json:"items"`
	}
	raw := strings.TrimSpace(envelope.Result.Response)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	if err = json.Unmarshal([]byte(strings.TrimSpace(raw)), &parsed); err != nil {
		return nil, err
	}
	out := []Candidate{}
	for _, c := range parsed.Items {
		if c.DurationDays < 1 || c.DurationDays > 365 || c.Unit != "BUSINESS_DAYS" || c.Action == "" || c.Evidence == "" || !strings.Contains(text, c.Evidence) {
			continue
		}
		c.Method = "LLM"
		c.RuleVersion = model
		out = append(out, c)
	}
	return out, nil
}
