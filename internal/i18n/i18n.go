// Package i18n translates the platform's messages for people: the API's
// answers to a Spanish browser, and alert emails.
//
// Messages are written in English with M, which is fmt.Sprintf and also
// marks the format as translatable. Translation happens when a message is
// shown, not when it is made: Translate matches a finished English message
// against the known formats and formats the Spanish one with the same
// values. So messages stored long ago (run results, release verdicts,
// problems) translate too, and code that makes messages needs no language.
//
// Spanish is Mexican Spanish (es-MX), formal (usted), technical terms in
// Spanish; see es.go. TestCatalog checks every M format has its Spanish.
package i18n

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Lang is a language the platform speaks.
type Lang string

const (
	English Lang = "en"
	Spanish Lang = "es"
)

// M formats a message for people. It is fmt.Sprintf; the format is a key
// of the Spanish catalog.
func M(format string, args ...any) string {
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

// Parse reads a language setting or an Accept-Language header: Spanish if
// it starts with "es", else English.
func Parse(s string) Lang {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "es") {
		return Spanish
	}
	return English
}

// FromRequest is the language the browser asked for.
func FromRequest(r *http.Request) Lang { return Parse(r.Header.Get("Accept-Language")) }

// pattern is one catalog entry, compiled to match finished messages.
type pattern struct {
	re *regexp.Regexp
	es string // the Spanish format
	n  int    // number of verbs
}

var (
	compileOnce sync.Once
	exact       map[string]string // formats without verbs
	patterns    []pattern         // formats with verbs, longest first
	memo        sync.Map          // finished English → Spanish ("" when unknown)
	memoSize    atomic.Int64      // entries in memo; it is emptied past memoCap
)

// memoCap bounds the memo: run messages are many and mostly unique.
const memoCap = 20000

// verb matches the fmt verbs the catalog uses; a Spanish format may pick
// values out of order with an index (%[2]s).
var verb = regexp.MustCompile(`%(\[[0-9]+\])?[-+# 0]*[0-9]*(?:\.[0-9]+)?[vsdqfgxw]`)

// verbs finds the verbs of a format, skipping literal percent signs (%%).
func verbs(format string) [][]int {
	return verb.FindAllStringIndex(strings.ReplaceAll(format, "%%", "\x00\x00"), -1)
}

func compile() {
	exact = map[string]string{}
	for en, es := range catalog {
		locs := verbs(en)
		if len(locs) == 0 {
			exact[strings.ReplaceAll(en, "%%", "%")] = es
			continue
		}
		var b strings.Builder
		b.WriteString("^")
		last := 0
		for _, l := range locs {
			b.WriteString(regexp.QuoteMeta(strings.ReplaceAll(en[last:l[0]], "%%", "%")))
			b.WriteString("(.+?)")
			last = l[1]
		}
		b.WriteString(regexp.QuoteMeta(strings.ReplaceAll(en[last:], "%%", "%")))
		b.WriteString("$")
		patterns = append(patterns, pattern{re: regexp.MustCompile("(?s)" + b.String()), es: es, n: len(locs)})
	}
	// Longer formats are more specific: try them first (ties alphabetically,
	// so the order never depends on map iteration).
	sort.Slice(patterns, func(i, j int) bool {
		a, b := patterns[i].re.String(), patterns[j].re.String()
		if len(a) != len(b) {
			return len(a) > len(b)
		}
		return a < b
	})
}

// Translate returns msg in lang. English, an empty message, or one the
// catalog does not know come back unchanged.
func Translate(lang Lang, msg string) string {
	if lang != Spanish || msg == "" {
		return msg
	}
	if v, ok := memo.Load(msg); ok {
		if s := v.(string); s != "" {
			return s
		}
		return msg
	}
	compileOnce.Do(compile)
	out := ""
	if es, ok := exact[msg]; ok {
		out = es
	} else {
		for _, p := range patterns {
			m := p.re.FindStringSubmatch(msg)
			if m == nil {
				continue
			}
			args := make([]any, p.n)
			for i := range args {
				// A value may itself be a known message ("...: <reason>").
				args[i] = Translate(lang, m[i+1])
			}
			out = sprintfStrings(p.es, args)
			break
		}
	}
	if out == "" {
		// A list of messages ("a; b" or one per line) translates part by part.
		for _, sep := range []string{"\n", "; "} {
			if parts := strings.Split(msg, sep); len(parts) > 1 {
				changed := false
				for i, p := range parts {
					if t := Translate(lang, p); t != p {
						parts[i], changed = t, true
					}
				}
				if changed {
					out = strings.Join(parts, sep)
				}
				break
			}
		}
	}
	if memoSize.Add(1) > memoCap {
		memo.Clear()
		memoSize.Store(0)
	}
	memo.Store(msg, out)
	if out == "" {
		return msg
	}
	return out
}

// sprintfStrings formats the Spanish format with values captured as text:
// every verb becomes %s, so numbers keep the form they had.
func sprintfStrings(format string, args []any) string {
	masked := verb.ReplaceAllString(strings.ReplaceAll(format, "%%", "\x00\x00"), "%${1}s")
	return fmt.Sprintf(strings.ReplaceAll(masked, "\x00\x00", "%%"), args...)
}

// TranslateExact translates only a message the catalog has word for word
// (labels and names), never through a pattern.
func TranslateExact(lang Lang, msg string) string {
	if lang != Spanish || msg == "" {
		return msg
	}
	compileOnce.Do(compile)
	if es, ok := exact[msg]; ok {
		return es
	}
	return msg
}

// T translates when the language is Spanish; a shorthand for handlers.
func (l Lang) T(msg string) string { return Translate(l, msg) }
