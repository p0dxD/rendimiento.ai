package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/p0dxD/rendimiento.ai/internal/i18n"
)

// Keys whose values are sentences for people: translated with the catalog's
// patterns (messages made with values, like "released #12").
var translatedKeys = map[string]bool{
	"message": true, "summary": true, "fix": true, "detail": true, "details": true,
	"reason": true, "reasons": true, "verifyMessage": true, "resolution": true,
	"description": true, "error": true, "result": true,
}

// Keys whose values are short labels: translated word for word or through a
// pattern with enough fixed text (i18n.TranslateLabel), so names people
// chose stay as they are.
var labelKeys = map[string]bool{"name": true, "title": true, "label": true, "kind": true}

// localized translates the JSON answers of /api/ for a browser that asks
// for Spanish (Accept-Language). Streams (live events) and logs pass as they
// are; so does everything when the language is English.
func localized(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := i18n.FromRequest(r)
		if lang != i18n.Spanish || !strings.HasPrefix(r.URL.Path, "/api/") ||
			strings.HasSuffix(r.URL.Path, "/events") || strings.HasSuffix(r.URL.Path, "/log") || r.URL.Path == "/api/public/stats" {
			next.ServeHTTP(w, r)
			return
		}
		rec := &bufferedWriter{header: w.Header(), status: http.StatusOK}
		next.ServeHTTP(rec, r)
		body := rec.body.Bytes()
		if strings.Contains(w.Header().Get("Content-Type"), "json") {
			var v any
			if json.Unmarshal(body, &v) == nil {
				if out, err := json.Marshal(translateJSON(lang, "", v)); err == nil {
					body = append(out, '\n')
				}
			}
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(rec.status)
		_, _ = w.Write(body)
	})
}

// translateJSON walks a decoded JSON value; key is the object key the value
// sits under (array elements inherit it, so "details": [...] translates).
func translateJSON(lang i18n.Lang, key string, v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = translateJSON(lang, k, e)
		}
		return x
	case []any:
		for i, e := range x {
			x[i] = translateJSON(lang, key, e)
		}
		return x
	case string:
		switch {
		case translatedKeys[key]:
			return i18n.Translate(lang, x)
		case labelKeys[key]:
			return i18n.TranslateLabel(lang, x)
		}
	}
	return v
}

// bufferedWriter holds a handler's answer so it can be translated.
type bufferedWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufferedWriter) Header() http.Header         { return b.header }
func (b *bufferedWriter) WriteHeader(status int)      { b.status = status }
func (b *bufferedWriter) Write(p []byte) (int, error) { return b.body.Write(p) }
