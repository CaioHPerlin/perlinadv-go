package google

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const Scope = "https://www.googleapis.com/auth/calendar.events https://www.googleapis.com/auth/calendar.calendarlist.readonly"

type Client struct {
	HTTP                                *http.Client
	ClientID, ClientSecret, RedirectURI string
	Key                                 []byte
	TokenURL, APIBaseURL                string
}

func New() (Client, error) {
	k, e := base64.StdEncoding.DecodeString(os.Getenv("GOOGLE_TOKEN_KEY"))
	if e != nil || len(k) != 32 {
		return Client{}, errors.New("GOOGLE_TOKEN_KEY must be 32 random bytes in base64")
	}
	return Client{HTTP: &http.Client{Timeout: 20 * time.Second}, ClientID: os.Getenv("GOOGLE_CLIENT_ID"), ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"), RedirectURI: os.Getenv("GOOGLE_REDIRECT_URI"), Key: k, TokenURL: "https://oauth2.googleapis.com/token", APIBaseURL: "https://www.googleapis.com/calendar/v3"}, nil
}
func (c Client) Configured() bool {
	return c.ClientID != "" && c.ClientSecret != "" && c.RedirectURI != ""
}
func (c Client) AuthURL(state string) string {
	v := url.Values{"client_id": {c.ClientID}, "redirect_uri": {c.RedirectURI}, "response_type": {"code"}, "scope": {Scope}, "access_type": {"offline"}, "prompt": {"consent"}, "state": {state}}
	return "https://accounts.google.com/o/oauth2/v2/auth?" + v.Encode()
}
func (c Client) Encrypt(plain string) ([]byte, error) {
	block, e := aes.NewCipher(c.Key)
	if e != nil {
		return nil, e
	}
	a, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, a.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	return a.Seal(nonce, nonce, []byte(plain), nil), nil
}
func (c Client) Decrypt(data []byte) (string, error) {
	block, e := aes.NewCipher(c.Key)
	if e != nil {
		return "", e
	}
	a, e := cipher.NewGCM(block)
	if e != nil {
		return "", e
	}
	if len(data) < a.NonceSize() {
		return "", errors.New("short ciphertext")
	}
	v, e := a.Open(nil, data[:a.NonceSize()], data[a.NonceSize():], nil)
	return string(v), e
}

type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Error        string `json:"error"`
}

func (c Client) tokenRequest(ctx context.Context, v url.Values) (Token, error) {
	v.Set("client_id", c.ClientID)
	v.Set("client_secret", c.ClientSecret)
	tokenURL := c.TokenURL
	if tokenURL == "" {
		tokenURL = "https://oauth2.googleapis.com/token"
	}
	req, e := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(v.Encode()))
	if e != nil {
		return Token{}, e
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return Token{}, e
	}
	defer resp.Body.Close()
	var t Token
	if e = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&t); e != nil {
		return t, e
	}
	if resp.StatusCode != 200 {
		return t, fmt.Errorf("Google token status %d: %s", resp.StatusCode, t.Error)
	}
	return t, nil
}
func (c Client) Exchange(ctx context.Context, code string) (Token, error) {
	return c.tokenRequest(ctx, url.Values{"code": {code}, "grant_type": {"authorization_code"}, "redirect_uri": {c.RedirectURI}})
}
func (c Client) Refresh(ctx context.Context, refresh string) (Token, error) {
	return c.tokenRequest(ctx, url.Values{"refresh_token": {refresh}, "grant_type": {"refresh_token"}})
}
func (c Client) Call(ctx context.Context, access, method, path string, body any) (int, []byte, error) {
	var data io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return 0, nil, e
		}
		data = bytes.NewReader(b)
	}
	apiBase := c.APIBaseURL
	if apiBase == "" {
		apiBase = "https://www.googleapis.com/calendar/v3"
	}
	req, e := http.NewRequestWithContext(ctx, method, apiBase+path, data)
	if e != nil {
		return 0, nil, e
	}
	req.Header.Set("Authorization", "Bearer "+access)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return 0, nil, e
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, b, e
}
