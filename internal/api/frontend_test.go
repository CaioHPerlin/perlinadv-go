package api

import (
	"net/url"
	"testing"
)

func TestFrontendURL(t *testing.T) {
	t.Run("builds a configured frontend link with escaped parameters", func(t *testing.T) {
		t.Setenv("WEB_APP_URL", "https://app.example.test/")
		got, err := frontendURL("/reset-password", url.Values{"token": {"a b&c"}})
		if err != nil {
			t.Fatal(err)
		}
		want := "https://app.example.test/reset-password?token=a+b%26c"
		if got != want {
			t.Fatalf("frontendURL() = %q, want %q", got, want)
		}
	})

	t.Run("allows an unset origin for backwards compatibility", func(t *testing.T) {
		t.Setenv("WEB_APP_URL", "")
		got, err := frontendURL("/calendar", url.Values{"google": {"connected"}})
		if err != nil || got != "" {
			t.Fatalf("frontendURL() = %q, %v; want empty URL without error", got, err)
		}
	})

	for _, invalid := range []string{
		"javascript:alert(1)",
		"https://user:pass@app.example.test",
		"https://app.example.test/other",
		"https://app.example.test/?next=https://evil.test",
		"https://app.example.test/#fragment",
	} {
		t.Run("rejects "+invalid, func(t *testing.T) {
			t.Setenv("WEB_APP_URL", invalid)
			if _, err := frontendURL("/calendar", url.Values{"google": {"connected"}}); err == nil {
				t.Fatalf("frontendURL(%q) accepted an invalid origin", invalid)
			}
		})
	}
}
