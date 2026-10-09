package dns

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DDNSTag in a Cloudflare record's comment opts a hand-made record into
// following the public IP (e.g. sites deployed before rendimiento).
const DDNSTag = "rendimiento-ddns"

type ddnsState struct {
	ip        string
	checkedAt time.Time
	changedAt time.Time
	err       error
}

// DDNSStatus is shown on the environment page.
type DDNSStatus struct {
	Enabled   bool      `json:"enabled"`
	IP        string    `json:"ip,omitempty"`
	CheckedAt time.Time `json:"checkedAt,omitempty"`
	ChangedAt time.Time `json:"changedAt,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// auto: records follow the public IP (DNS_TARGET=auto).
func (c *Cloudflare) auto() bool { return strings.EqualFold(c.Target, "auto") }

// DDNSStatus reports the last public-IP check and change, for the Environment page.
func (c *Cloudflare) DDNSStatus() DDNSStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := DDNSStatus{Enabled: c.auto(), IP: c.ddns.ip, CheckedAt: c.ddns.checkedAt, ChangedAt: c.ddns.changedAt}
	if c.ddns.err != nil {
		st.Error = c.ddns.err.Error()
	}
	return st
}

// target is what new records point at: the configured value, or the
// detected public IP in auto mode (detecting it on first use).
func (c *Cloudflare) target(ctx context.Context) (string, error) {
	if !c.auto() {
		return c.Target, nil
	}
	c.mu.Lock()
	ip := c.ddns.ip
	c.mu.Unlock()
	if ip != "" {
		return ip, nil
	}
	if _, err := c.Sync(ctx); err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ddns.ip == "" {
		return "", errors.New("public IP not detected yet")
	}
	return c.ddns.ip, nil
}

// SyncResult reports what one dynamic DNS pass did.
type SyncResult struct {
	IP      string   `json:"ip"`
	Changed bool     `json:"changed"`
	Updated []string `json:"updated"` // hostnames repointed
}

// Sync detects the public IP and repoints every A record that follows it:
// records rendimiento created and records tagged with DDNSTag. Other
// records are never touched. In fixed-target mode it is a no-op.
func (c *Cloudflare) Sync(ctx context.Context) (*SyncResult, error) {
	if !c.auto() {
		return &SyncResult{IP: c.Target}, nil
	}
	detect := c.DetectIP
	if detect == nil {
		detect = PublicIPv4
	}
	ip, err := detect(ctx)
	c.mu.Lock()
	c.ddns.checkedAt = time.Now()
	c.ddns.err = err
	prev := c.ddns.ip
	if err == nil && ip != prev {
		c.ddns.ip, c.ddns.changedAt = ip, time.Now()
	}
	c.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("detect public IP: %w", err)
	}
	res := &SyncResult{IP: ip, Changed: prev != "" && prev != ip}

	// Always reconcile records, not only on change: a failed update earlier,
	// or a record edited by hand, gets corrected on the next pass.
	if _, err := c.Zones(ctx); err != nil {
		return res, err
	}
	c.mu.Lock()
	zones := make(map[string]string, len(c.zones))
	for k, v := range c.zones {
		zones[k] = v
	}
	c.mu.Unlock()
	var errs []error
	for zoneName, zoneID := range zones {
		recs, err := c.aRecords(ctx, zoneID)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", zoneName, err))
			continue
		}
		for _, r := range recs {
			follows := strings.HasPrefix(r.Comment, marker) || strings.Contains(r.Comment, DDNSTag)
			if !follows || r.Content == ip {
				continue
			}
			r.Content = ip
			if err := c.do(ctx, http.MethodPut, "/zones/"+zoneID+"/dns_records/"+r.ID, r, nil); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", r.Name, err))
				continue
			}
			res.Updated = append(res.Updated, r.Name)
		}
	}
	if len(errs) > 0 {
		err := errors.Join(errs...)
		c.mu.Lock()
		c.ddns.err = err
		c.mu.Unlock()
		return res, err
	}
	return res, nil
}

// aRecords lists every A record in a zone, following pagination.
func (c *Cloudflare) aRecords(ctx context.Context, zoneID string) ([]record, error) {
	var all []record
	for page := 1; page <= 50; page++ {
		var recs []record
		q := url.Values{"type": {"A"}, "per_page": {"500"}, "page": {fmt.Sprint(page)}}
		if err := c.do(ctx, http.MethodGet, "/zones/"+zoneID+"/dns_records?"+q.Encode(), nil, &recs); err != nil {
			return nil, err
		}
		all = append(all, recs...)
		if len(recs) < 500 {
			break
		}
	}
	return all, nil
}

// Run keeps records following the public IP until ctx ends.
func (c *Cloudflare) Run(ctx context.Context, every time.Duration, log func(msg string, args ...any)) {
	if !c.auto() {
		return
	}
	for {
		res, err := c.Sync(ctx)
		switch {
		case err != nil:
			log("dynamic DNS sync failed", "err", err)
		case res.Changed || len(res.Updated) > 0:
			log("dynamic DNS updated", "ip", res.IP, "changed", res.Changed, "records", res.Updated)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// ipSources are asked in order; each returns this network's public IPv4.
var ipSources = []string{
	"https://1.1.1.1/cdn-cgi/trace", // Cloudflare: "ip=<addr>" line
	"https://api4.ipify.org",        // plain text, IPv4 only
}

// PublicIPv4 returns this network's public IPv4 address.
func PublicIPv4(ctx context.Context) (string, error) {
	hc := &http.Client{Timeout: 5 * time.Second}
	var errs []error
	for _, src := range ipSources {
		ip, err := fetchIP(ctx, hc, src)
		if err == nil {
			return ip, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", src, err))
	}
	return "", errors.Join(errs...)
}

// fetchIP asks one public service what our IP is.
func fetchIP(ctx context.Context, hc *http.Client, src string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	return ParseIPResponse(string(body))
}

// ParseIPResponse accepts either a bare address or Cloudflare's trace format,
// and only returns a public IPv4 address, so a misconfigured proxy or a
// private address can never be published in DNS.
func ParseIPResponse(body string) (string, error) {
	candidate := strings.TrimSpace(body)
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "ip="); ok {
			candidate = strings.TrimSpace(v)
		}
	}
	ip := net.ParseIP(candidate)
	switch {
	case ip == nil:
		return "", fmt.Errorf("no IP address in response")
	case ip.To4() == nil:
		return "", fmt.Errorf("got IPv6 %s; A records need IPv4", ip)
	case ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || !ip.IsGlobalUnicast():
		return "", fmt.Errorf("refusing non-public address %s", ip)
	}
	return ip.String(), nil
}
