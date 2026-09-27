package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

func errorsAs(err error, target any) bool { return err != nil && errors.As(err, target) }

// ---- manifest flow: create the GitHub App with one click ----

// Manifest describes the app GitHub should create. Posting it from a browser
// form to https://github.com/settings/apps/new returns a code to our redirect URL.
func Manifest(name, baseURL string) map[string]any {
	return map[string]any{
		"name":         name,
		"url":          baseURL,
		"redirect_url": baseURL + "/api/setup/github/callback",
		"callback_urls": []string{
			baseURL + "/api/auth/callback",
		},
		"public": false,
		"hook_attributes": map[string]any{
			"url":    baseURL + "/api/webhooks/github",
			"active": true,
		},
		"default_permissions": map[string]string{
			"contents":      "write", // read code, push the onboarding branch
			"pull_requests": "write", // open the onboarding PR
			"checks":        "write", // report CI status
			"metadata":      "read",
			// Renovate add-on: its dependency dashboard is an issue, and it
			// reads commit statuses before auto-merging.
			"issues":   "write",
			"statuses": "read",
		},
		"default_events": []string{"push"},
	}
}

// ConvertManifest exchanges the one-time code for the new app's credentials.
func ConvertManifest(ctx context.Context, apiBase, code string) (*Credentials, error) {
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	a := &App{BaseURL: apiBase}
	var c Credentials
	if err := a.do(ctx, "", http.MethodPost, "/app-manifests/"+url.PathEscape(code)+"/conversions", nil, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// ---- user login (OAuth via the same GitHub App) ----

func (a *App) AuthorizeURL(state, redirect string) string {
	q := url.Values{"client_id": {a.creds.ClientID}, "state": {state}, "redirect_uri": {redirect}}
	return "https://github.com/login/oauth/authorize?" + q.Encode()
}

// OAuthBase is overridden in tests.
var OAuthBase = "https://github.com"

// Login exchanges an OAuth code for the user's login name.
func (a *App) Login(ctx context.Context, code string) (string, error) {
	form := url.Values{"client_id": {a.creds.ClientID}, "client_secret": {a.creds.ClientSecret}, "code": {code}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, OAuthBase+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := a.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var tok struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error_description"`
	}
	if err := decodeJSON(resp, &tok); err != nil {
		return "", err
	}
	if tok.AccessToken == "" {
		return "", errors.New("github login failed: " + tok.Error)
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := a.do(ctx, "Bearer "+tok.AccessToken, http.MethodGet, "/user", nil, &user); err != nil {
		return "", err
	}
	return user.Login, nil
}

func decodeJSON(resp *http.Response, out any) error {
	if resp.StatusCode >= 300 {
		return &APIError{Status: resp.StatusCode, Method: resp.Request.Method, Path: resp.Request.URL.Path}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
