// Package github talks to GitHub as a GitHub App: short-lived installation
// tokens instead of personal access tokens, webhooks delivered for every
// installed repo, check runs for CI status and PRs for onboarding.
package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Credentials are produced once by the manifest flow and stored in a Kubernetes Secret.
type Credentials struct {
	AppID         int64  `json:"id"`
	Slug          string `json:"slug"`
	PrivateKey    string `json:"pem"`
	WebhookSecret string `json:"webhook_secret"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	HTMLURL       string `json:"html_url"`
}

// App is the GitHub App: it signs JWTs with its private key and exchanges them for installation
// tokens.
type App struct {
	creds   Credentials
	key     *rsa.PrivateKey
	BaseURL string // API base; https://api.github.com by default
	HTTP    *http.Client

	mu     sync.Mutex
	tokens map[int64]cachedToken
}

type cachedToken struct {
	token   string
	expires time.Time
}

// New parses the App's private key.
func New(c Credentials) (*App, error) {
	block, _ := pem.Decode([]byte(c.PrivateKey))
	if block == nil {
		return nil, errors.New("github app private key is not PEM")
	}
	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = k
	} else if k, err2 := x509.ParsePKCS8PrivateKey(block.Bytes); err2 == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("github app key is not RSA")
		}
		key = rk
	} else {
		return nil, fmt.Errorf("parse github app key: %w", err)
	}
	return &App{creds: c, key: key, tokens: map[int64]cachedToken{}}, nil
}

// Credentials returns what the App was created with.
func (a *App) Credentials() Credentials { return a.creds }

func (a *App) base() string {
	if a.BaseURL != "" {
		return a.BaseURL
	}
	return "https://api.github.com"
}

func (a *App) client() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// JWT authenticates as the app itself (RS256, 9 minute lifetime).
func (a *App) JWT(now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(), // allow for clock drift
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": fmt.Sprint(a.creds.AppID),
	})
	signing := header + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

// InstallationToken returns a cached token for the installation, refreshing
// it five minutes before expiry. Tokens are valid for one hour.
func (a *App) InstallationToken(ctx context.Context, installationID int64) (string, error) {
	a.mu.Lock()
	if t, ok := a.tokens[installationID]; ok && time.Until(t.expires) > 5*time.Minute {
		a.mu.Unlock()
		return t.token, nil
	}
	a.mu.Unlock()
	jwt, err := a.JWT(time.Now())
	if err != nil {
		return "", err
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := a.do(ctx, "Bearer "+jwt, http.MethodPost, fmt.Sprintf("/app/installations/%d/access_tokens", installationID), nil, &out); err != nil {
		return "", err
	}
	a.mu.Lock()
	a.tokens[installationID] = cachedToken{out.Token, out.ExpiresAt}
	a.mu.Unlock()
	return out.Token, nil
}

// Installation is the App installed on one account (user or organization).
type Installation struct {
	ID      int64 `json:"id"`
	Account struct {
		Login string `json:"login"`
	} `json:"account"`
}

// Installations lists the accounts the App is installed on.
func (a *App) Installations(ctx context.Context) ([]Installation, error) {
	jwt, err := a.JWT(time.Now())
	if err != nil {
		return nil, err
	}
	var out []Installation
	return out, a.do(ctx, "Bearer "+jwt, http.MethodGet, "/app/installations?per_page=100", nil, &out)
}

// Client returns an API client acting as the given installation.
func (a *App) Client(installationID int64) *Client {
	return &Client{app: a, installation: installationID}
}

// do performs a JSON API call. A nil out discards the response body.
func (a *App) do(ctx context.Context, auth, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	url := path
	if !strings.HasPrefix(path, "http") {
		url = a.base() + path
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return &APIError{Status: resp.StatusCode, Method: method, Path: path, Body: string(msg)}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// APIError is a non-2xx response from the GitHub API.
type APIError struct {
	Status       int
	Method, Path string
	Body         string
}

// Error implements error.
func (e *APIError) Error() string {
	return fmt.Sprintf("github %s %s: %d %s", e.Method, e.Path, e.Status, e.Body)
}

// IsNotFound reports whether err is a GitHub 404.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

// VerifyWebhook checks X-Hub-Signature-256 in constant time.
func VerifyWebhook(secret string, body []byte, signature string) bool {
	sig, ok := strings.CutPrefix(signature, "sha256=")
	if !ok || secret == "" {
		return false
	}
	want, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}

// FreshInstallationToken mints a new token (valid one hour) instead of
// reusing a cached one, for jobs that need the full hour, such as Renovate.
func (a *App) FreshInstallationToken(ctx context.Context, installationID int64) (string, error) {
	jwt, err := a.JWT(time.Now())
	if err != nil {
		return "", err
	}
	var out struct {
		Token string `json:"token"`
	}
	return out.Token, a.do(ctx, "Bearer "+jwt, http.MethodPost, fmt.Sprintf("/app/installations/%d/access_tokens", installationID), nil, &out)
}

// InstallationPermissions are what the account owner granted, e.g.
// {"contents": "write", "issues": "write"}. They lag the app's requested
// permissions until the owner accepts a change.
func (a *App) InstallationPermissions(ctx context.Context, installationID int64) (map[string]string, error) {
	jwt, err := a.JWT(time.Now())
	if err != nil {
		return nil, err
	}
	var out struct {
		Permissions map[string]string `json:"permissions"`
	}
	return out.Permissions, a.do(ctx, "Bearer "+jwt, http.MethodGet, fmt.Sprintf("/app/installations/%d", installationID), nil, &out)
}

// BotIdentity is the app's bot user, as commits and PRs show it:
// login "<slug>[bot]" and a noreply email tied to its user ID.
type BotIdentity struct {
	Login, Email string
}

// Bot returns the App's bot user, as commits and pull requests show it.
func (a *App) Bot(ctx context.Context) (*BotIdentity, error) {
	jwt, err := a.JWT(time.Now())
	if err != nil {
		return nil, err
	}
	var app struct {
		Slug string `json:"slug"`
	}
	if err := a.do(ctx, "Bearer "+jwt, http.MethodGet, "/app", nil, &app); err != nil {
		return nil, err
	}
	login := app.Slug + "[bot]"
	var user struct {
		ID int64 `json:"id"`
	}
	if err := a.do(ctx, "", http.MethodGet, "/users/"+url.PathEscape(login), nil, &user); err != nil {
		return nil, err
	}
	return &BotIdentity{Login: login, Email: fmt.Sprintf("%d+%s@users.noreply.github.com", user.ID, login)}, nil
}
