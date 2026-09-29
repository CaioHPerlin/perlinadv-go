package api

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// frontendURL builds a link under the configured frontend origin. WEB_APP_URL
// is deployment configuration, never a value supplied by a request.
func frontendURL(path string, query url.Values) (string, error) {
	raw := strings.TrimSpace(os.Getenv("WEB_APP_URL"))
	if raw == "" {
		return "", nil
	}

	base, err := url.Parse(raw)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Hostname() == "" || base.User != nil || (base.Path != "" && base.Path != "/") || base.RawQuery != "" || base.Fragment != "" {
		return "", fmt.Errorf("WEB_APP_URL must be an http(s) origin")
	}
	base.Path = path
	base.RawQuery = query.Encode()
	return base.String(), nil
}
