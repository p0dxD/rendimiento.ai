// Package dns manages the public DNS record for each app domain.
package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	. "github.com/p0dxD/rendimiento.ai/internal/i18n" //nolint:revive // M marks messages for people
)

// --8<-- [start:provider]

// Provider creates and removes the record pointing a host at the cluster's ingress.
type Provider interface {
	// Ensure makes host resolve to the configured target. It reports
	// ErrConflict when a record exists that rendimiento did not create.
	Ensure(ctx context.Context, host, app string) error
	// Remove deletes the record for host if, and only if, rendimiento created it for app.
	Remove(ctx context.Context, host, app string) error
	// Zones lists the domains available for new apps (wizard dropdown).
	Zones(ctx context.Context) ([]string, error)
	// Describe reports which provider this is and whether it is usable, for
	// the environment page. Providers are swappable; callers never switch on type.
	Describe(ctx context.Context) Description
}

// --8<-- [end:provider]

// Description is a provider's self-reported identity and health.
type Description struct {
	ID      string   `json:"id"`   // cloudflare | manual | route53 | ...
	Name    string   `json:"name"` // display name
	Healthy bool     `json:"healthy"`
	Detail  string   `json:"detail"`
	Zones   []string `json:"zones,omitempty"`
}

var ErrConflict = errors.New("a DNS record not managed by rendimiento already exists for this host")

// Noop is used when no DNS provider is configured; records are managed by hand.
type Noop struct{}

// Ensure does nothing: records are managed by hand.
func (Noop) Ensure(context.Context, string, string) error { return nil }

// Remove does nothing.
func (Noop) Remove(context.Context, string, string) error { return nil }

// Zones returns none.
func (Noop) Zones(context.Context) ([]string, error) { return nil, nil }

// Describe reports that no DNS provider is configured.
func (Noop) Describe(context.Context) Description {
	return Description{ID: "manual", Name: M("Manual DNS"), Healthy: false,
		Detail: M("No DNS provider configured: create a DNS record by hand for each app domain.")}
}

// Cloudflare implements Provider with the Cloudflare v4 API and a scoped API token
// (Zone:Read + DNS:Edit).
type Cloudflare struct {
	Token string
	// Target is where app hosts point: an IP (A record), a hostname
	// (CNAME), or "auto" to follow this network's public IP (dynamic DNS).
	Target  string
	Proxied bool
	BaseURL string // defaults to the public API; overridden in tests
	HTTP    *http.Client
	// DetectIP finds the current public IPv4 in auto mode; defaults to PublicIPv4.
	DetectIP func(ctx context.Context) (string, error)

	mu    sync.Mutex
	zones map[string]string // zone name → id
	ddns  ddnsState
}

const marker = "managed-by=rendimiento"

func comment(app string) string { return marker + " app=" + app }

type record struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl"`
	Comment string `json:"comment"`
}

func (c *Cloudflare) recordType() string {
	if c.auto() || net.ParseIP(c.Target) != nil {
		return "A"
	}
	return "CNAME"
}

// Ensure creates or updates the record for host, marked as managed for app; it refuses to change a
// record another app or a person created.
func (c *Cloudflare) Ensure(ctx context.Context, host, app string) error {
	zoneID, err := c.zoneFor(ctx, host)
	if err != nil {
		return err
	}
	target, err := c.target(ctx)
	if err != nil {
		return err
	}
	want := record{Type: c.recordType(), Name: host, Content: target, Proxied: c.Proxied, TTL: 1, Comment: comment(app)}
	existing, err := c.find(ctx, zoneID, host)
	if err != nil {
		return err
	}
	switch {
	case existing == nil:
		return c.do(ctx, http.MethodPost, "/zones/"+zoneID+"/dns_records", want, nil)
	case existing.Comment == want.Comment:
		if existing.Type == want.Type && existing.Content == want.Content && existing.Proxied == want.Proxied {
			return nil
		}
		return c.do(ctx, http.MethodPut, "/zones/"+zoneID+"/dns_records/"+existing.ID, want, nil)
	case strings.HasPrefix(existing.Comment, marker):
		return fmt.Errorf("%w: %s belongs to another rendimiento app (%s)", ErrConflict, host, existing.Comment)
	case existing.Content == want.Content:
		return nil // hand-made record already points at us; use it but never touch it
	default:
		return fmt.Errorf("%w: %s → %s", ErrConflict, host, existing.Content)
	}
}

// Describe verifies the API token and lists the zones it can manage.
func (c *Cloudflare) Describe(ctx context.Context) Description {
	d := Description{ID: "cloudflare", Name: "Cloudflare"}
	var tok struct {
		Status string `json:"status"`
	}
	if err := c.do(ctx, http.MethodGet, "/user/tokens/verify", nil, &tok); err != nil {
		d.Detail = M("API token rejected: %s", err.Error())
		return d
	}
	if tok.Status != "active" {
		d.Detail = M("API token is %s", tok.Status)
		return d
	}
	zones, err := c.Zones(ctx)
	if err != nil {
		d.Detail = M("token is valid but zones could not be listed: %s", err.Error())
		return d
	}
	d.Healthy, d.Zones = true, zones
	kind := "A"
	if c.recordType() == "CNAME" {
		kind = "CNAME"
	}
	target := c.Target
	if c.auto() {
		st := c.DDNSStatus()
		target = M("public IP %s (auto-detected)", st.IP)
		if st.IP == "" {
			target = M("public IP (not detected yet)")
		}
		if st.Error != "" {
			d.Healthy = false
			d.Detail = M("token active; %d zone(s); public IP detection failing: %s", len(zones), st.Error)
			return d
		}
	}
	d.Detail = M("token active; %d zone(s); new apps get %s records → %s", len(zones), kind, target)
	if c.Proxied {
		d.Detail = M("%s (proxied)", d.Detail)
	}
	return d
}

// Remove deletes host's record if it is managed for app.
func (c *Cloudflare) Remove(ctx context.Context, host, app string) error {
	zoneID, err := c.zoneFor(ctx, host)
	if err != nil {
		return err
	}
	existing, err := c.find(ctx, zoneID, host)
	if err != nil || existing == nil || existing.Comment != comment(app) {
		return err
	}
	return c.do(ctx, http.MethodDelete, "/zones/"+zoneID+"/dns_records/"+existing.ID, nil, nil)
}

// Zones lists the zones the token can edit, for the domain picker.
func (c *Cloudflare) Zones(ctx context.Context) ([]string, error) {
	var zones []struct{ ID, Name string }
	if err := c.do(ctx, http.MethodGet, "/zones?status=active&per_page=50", nil, &zones); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.zones == nil {
		c.zones = map[string]string{}
	}
	names := make([]string, 0, len(zones))
	for _, z := range zones {
		c.zones[z.Name] = z.ID
		names = append(names, z.Name)
	}
	return names, nil
}

// zoneFor finds the most specific zone that contains host.
func (c *Cloudflare) zoneFor(ctx context.Context, host string) (string, error) {
	c.mu.Lock()
	cached := len(c.zones) > 0
	c.mu.Unlock()
	if !cached {
		if _, err := c.Zones(ctx); err != nil {
			return "", err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for name := host; strings.Contains(name, "."); name = name[strings.Index(name, ".")+1:] {
		if id, ok := c.zones[name]; ok {
			return id, nil
		}
	}
	return "", fmt.Errorf("no Cloudflare zone found for %s", host)
}

func (c *Cloudflare) find(ctx context.Context, zoneID, host string) (*record, error) {
	var recs []record
	q := url.Values{"name": {host}}
	if err := c.do(ctx, http.MethodGet, "/zones/"+zoneID+"/dns_records?"+q.Encode(), nil, &recs); err != nil {
		return nil, err
	}
	for _, r := range recs {
		if r.Type == "A" || r.Type == "AAAA" || r.Type == "CNAME" {
			return &r, nil
		}
	}
	return nil, nil
}

func (c *Cloudflare) do(ctx context.Context, method, path string, body, out any) error {
	base := c.BaseURL
	if base == "" {
		base = "https://api.cloudflare.com/client/v4"
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var env struct {
		Success bool                       `json:"success"`
		Errors  []struct{ Message string } `json:"errors"`
		Result  json.RawMessage            `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("cloudflare %s %s: %s: %w", method, path, resp.Status, err)
	}
	if !env.Success {
		msgs := make([]string, len(env.Errors))
		for i, e := range env.Errors {
			msgs[i] = e.Message
		}
		return fmt.Errorf("cloudflare %s %s: %s", method, path, strings.Join(msgs, "; "))
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}
