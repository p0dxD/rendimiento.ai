// Package notify emails the platform's owner about what needs attention:
// releases rolled back by verification, failed runs on the default branch,
// outages and recoveries. Messages are rendered as an email-safe HTML page
// (tables and inline styles) with a plain-text alternative, and sent with
// Resend's HTTP API.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/i18n"
)

// Tone is a message's severity: it sets its color and label.
type Tone string

// Tones.
const (
	Critical Tone = "critical"
	Warning  Tone = "warning"
	Good     Tone = "good"
	Info     Tone = "info"
)

// Fact is one row of a message's details table.
type Fact struct{ Label, Value string }

// Message is one notification.
type Message struct {
	Tone    Tone
	Subject string // the email's subject line
	Title   string // the heading inside the email
	Summary string // one or two sentences under it
	Facts   []Fact
	// Details is extra text shown in a monospace box (an error, a reason).
	Details     string
	ActionURL   string
	ActionLabel string
	// Key identifies the event for de-duplication: a message with the same
	// key as one sent within Cooldown is dropped. Empty never de-duplicates.
	Key string
}

// Sender delivers a rendered email.
type Sender interface {
	Send(ctx context.Context, to []string, subject, html, text string) error
}

// Notifier renders messages and sends them, de-duplicated and rate-limited.
// A nil *Notifier, or one without a Sender or recipients, does nothing, so
// callers never check whether notifications are configured.
type Notifier struct {
	Sender Sender
	To     []string
	Log    *slog.Logger
	// Cooldown drops a message whose Key was sent this recently (default 30m).
	Cooldown time.Duration
	// PerHour caps emails per rolling hour (default 20); past it, messages
	// are dropped and logged, so a bad night cannot flood the inbox.
	PerHour int
	// OnSent, if set, is called after each email that was sent; the
	// platform uses it to resolve earlier send failures (MsgSendFailed).
	OnSent func()
	// Lang is the language of the emails (NOTIFY_LANG); English by default.
	Lang i18n.Lang

	mu   sync.Mutex
	sent map[string]time.Time
	hour []time.Time
}

// Enabled reports whether messages are actually sent.
func (n *Notifier) Enabled() bool { return n != nil && n.Sender != nil && len(n.To) > 0 }

// Notify sends m in the background (a slow mail API never delays the
// caller). It reports false when m was dropped: notifications off, a
// duplicate, or over the hourly cap.
func (n *Notifier) Notify(m Message) bool {
	if !n.allow(m) {
		return false
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := n.Send(ctx, m); err != nil && n.Log != nil {
			n.Log.Warn(MsgSendFailed, "subject", m.Subject, "err", err)
		}
	}()
	return true
}

// allow keeps email from flooding: the same message (by its key) at most
// once every 30 minutes, and 20 emails an hour in all, by default.
func (n *Notifier) allow(m Message) bool {
	if !n.Enabled() {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	now := time.Now()
	cooldown := n.Cooldown
	if cooldown == 0 {
		cooldown = 30 * time.Minute
	}
	if n.sent == nil {
		n.sent = map[string]time.Time{}
	}
	if m.Key != "" {
		if at, ok := n.sent[m.Key]; ok && now.Sub(at) < cooldown {
			return false
		}
	}
	limit := n.PerHour
	if limit == 0 {
		limit = 20
	}
	recent := n.hour[:0]
	for _, t := range n.hour {
		if now.Sub(t) < time.Hour {
			recent = append(recent, t)
		}
	}
	n.hour = recent
	if len(n.hour) >= limit {
		if n.Log != nil {
			n.Log.Warn("notification dropped: hourly email limit reached", "subject", m.Subject, "limit", limit)
		}
		return false
	}
	n.hour = append(n.hour, now)
	if m.Key != "" {
		n.sent[m.Key] = now
	}
	return true
}

// Send renders and sends m now, without de-duplication (the test email).
func (n *Notifier) Send(ctx context.Context, m Message) error {
	if !n.Enabled() {
		return errors.New("email notifications are not configured (NOTIFY_EMAIL_TO and RESEND_API_KEY)")
	}
	html, text, err := render(Localize(m, n.Lang), n.Lang)
	if err != nil {
		return err
	}
	if err := n.Sender.Send(ctx, n.To, i18n.Translate(n.Lang, m.Subject), html, text); err != nil {
		return err
	}
	if n.OnSent != nil {
		n.OnSent()
	}
	return nil
}

// MsgSendFailed is the problem recorded when an email could not be sent.
const MsgSendFailed = "notification email failed"

// ---- rendering ----

var tones = map[Tone]struct{ Color, Soft, Label string }{
	Critical: {"#d03b3b", "#fdecec", i18n.M("Needs attention")},
	Warning:  {"#b7791f", "#fdf4e3", i18n.M("Warning")},
	Good:     {"#0f8a0f", "#e8f6e8", i18n.M("Resolved")},
	Info:     {"#2a78d6", "#e9f1fb", i18n.M("Info")},
}

// Localize translates a message's text for people into lang: subject,
// title, summary, facts, details (line by line) and the action.
func Localize(m Message, lang i18n.Lang) Message {
	if lang != i18n.Spanish {
		return m
	}
	tr := func(s string) string { return i18n.Translate(lang, s) }
	m.Subject, m.Title, m.Summary, m.ActionLabel = tr(m.Subject), tr(m.Title), tr(m.Summary), tr(m.ActionLabel)
	facts := make([]Fact, len(m.Facts))
	for i, f := range m.Facts {
		facts[i] = Fact{Label: tr(f.Label), Value: tr(f.Value)}
	}
	m.Facts = facts
	lines := strings.Split(m.Details, "\n")
	for i, l := range lines {
		lines[i] = tr(l)
	}
	m.Details = strings.Join(lines, "\n")
	return m
}

// Render returns m as an email-safe HTML page and as plain text, in English.
func Render(m Message) (string, string, error) { return render(m, i18n.English) }

// render is an email's subject and HTML in a language, in the tone's colors.
func render(m Message, lang i18n.Lang) (string, string, error) {
	t, ok := tones[m.Tone]
	if !ok {
		t = tones[Info]
	}
	label := i18n.Translate(lang, t.Label)
	footer := i18n.Translate(lang, i18n.M("Sent by rendimiento.ai, your CI/CD platform. You get these because you are set as NOTIFY_EMAIL_TO."))
	var html bytes.Buffer
	err := page.Execute(&html, struct {
		Message
		Color, Soft, Label, Lang, Footer string
	}{m, t.Color, t.Soft, label, string(lang), footer})
	if err != nil {
		return "", "", err
	}
	var text strings.Builder
	fmt.Fprintf(&text, "%s\n%s\n\n%s\n", strings.ToUpper(label), m.Title, m.Summary)
	if len(m.Facts) > 0 {
		text.WriteString("\n")
		for _, f := range m.Facts {
			fmt.Fprintf(&text, "%s: %s\n", f.Label, f.Value)
		}
	}
	if m.Details != "" {
		fmt.Fprintf(&text, "\n%s\n", m.Details)
	}
	if m.ActionURL != "" {
		fmt.Fprintf(&text, "\n%s: %s\n", m.ActionLabel, m.ActionURL)
	}
	text.WriteString("\n-- \n" + i18n.Translate(lang, i18n.M("rendimiento.ai · sent by your CI/CD platform")) + "\n")
	return html.String(), text.String(), nil
}

// The layout uses tables and inline styles only: what Gmail, Outlook and
// Apple Mail render the same way. 560px wide, fluid on phones.
var page = template.Must(template.New("email").Parse(`<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light"><meta name="supported-color-schemes" content="light">
<title>{{.Subject}}</title></head>
<body style="margin:0;padding:0;background:#f4f4f6;">
<div style="display:none;max-height:0;overflow:hidden;opacity:0;">{{.Summary}}</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f4f4f6;">
<tr><td align="center" style="padding:28px 12px;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:560px;background:#ffffff;border:1px solid #e3e3ea;border-radius:12px;overflow:hidden;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#16161d;">
    <tr><td style="height:6px;background:{{.Color}};font-size:0;line-height:0;">&nbsp;</td></tr>
    <tr><td style="padding:22px 28px 0;">
      <table role="presentation" cellpadding="0" cellspacing="0"><tr>
        <td style="background:#4f46e5;border-radius:6px;width:22px;height:22px;text-align:center;color:#fff;font-size:13px;font-weight:700;">r</td>
        <td style="padding-left:8px;font-size:14px;font-weight:600;color:#16161d;">rendimiento</td>
      </tr></table>
    </td></tr>
    <tr><td style="padding:18px 28px 0;">
      <span style="display:inline-block;padding:3px 10px;border-radius:999px;background:{{.Soft}};color:{{.Color}};font-size:12px;font-weight:600;letter-spacing:.02em;">&#9679;&nbsp;{{.Label}}</span>
      <h1 style="margin:12px 0 6px;font-size:21px;line-height:1.3;font-weight:700;color:#16161d;">{{.Title}}</h1>
      <p style="margin:0;font-size:15px;line-height:1.55;color:#3b3b47;">{{.Summary}}</p>
    </td></tr>
    {{if .Facts}}<tr><td style="padding:18px 28px 0;">
      <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="border:1px solid #ececf1;border-radius:8px;">
        {{range $i, $f := .Facts}}<tr>
          <td style="padding:9px 12px;font-size:13px;color:#6b6b7b;width:34%;vertical-align:top;{{if $i}}border-top:1px solid #ececf1;{{end}}">{{$f.Label}}</td>
          <td style="padding:9px 12px;font-size:14px;color:#16161d;vertical-align:top;{{if $i}}border-top:1px solid #ececf1;{{end}}">{{$f.Value}}</td>
        </tr>{{end}}
      </table>
    </td></tr>{{end}}
    {{if .Details}}<tr><td style="padding:14px 28px 0;">
      <div style="background:#f6f6f9;border-radius:8px;padding:12px 14px;font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:12.5px;line-height:1.5;color:#2b2b35;white-space:pre-wrap;word-break:break-word;">{{.Details}}</div>
    </td></tr>{{end}}
    {{if .ActionURL}}<tr><td style="padding:22px 28px 0;">
      <a href="{{.ActionURL}}" style="display:inline-block;background:#16161d;color:#ffffff;text-decoration:none;font-size:14px;font-weight:600;padding:11px 18px;border-radius:8px;">{{.ActionLabel}} &rarr;</a>
    </td></tr>{{end}}
    <tr><td style="padding:26px 28px 24px;">
      <p style="margin:0;font-size:12px;line-height:1.5;color:#8a8a99;border-top:1px solid #ececf1;padding-top:14px;">{{.Footer}}</p>
    </td></tr>
  </table>
</td></tr></table>
</body></html>`))

// ---- Resend ----

// Resend sends email through Resend's HTTP API (https://resend.com).
type Resend struct {
	APIKey string
	From   string // e.g. "rendimiento <alerts@joserod.space>", a verified sender
	// URL overrides the API endpoint (tests).
	URL  string
	HTTP *http.Client
}

// Send posts one email to Resend.
func (r *Resend) Send(ctx context.Context, to []string, subject, html, text string) error {
	body, _ := json.Marshal(map[string]any{"from": r.From, "to": to, "subject": subject, "html": html, "text": text})
	url := r.URL
	if url == "" {
		url = "https://api.resend.com/emails"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.APIKey)
	req.Header.Set("Content-Type", "application/json")
	// Resend sits behind Cloudflare, whose bot rules block some default
	// client user agents; name ourselves.
	req.Header.Set("User-Agent", "rendimiento/1 (+https://github.com/p0dxD/rendimiento.ai)")
	hc := r.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("resend: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}
