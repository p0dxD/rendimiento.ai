package i18n

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Every format the platform passes to M has its Spanish, with as many
// values; a new message without a translation fails here.
func TestCatalog(t *testing.T) {
	call := regexp.MustCompile(`(?:^|[^A-Za-z0-9_])(?:i18n\.)?M\(\s*("(?:[^"\\]|\\.)*")`)
	root := filepath.Join("..", "..")
	seen := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "web" || d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".")) && path != root {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range call.FindAllStringSubmatch(string(src), -1) {
			format, err := strconv.Unquote(m[1])
			if err != nil {
				t.Errorf("%s: %s: %v", path, m[1], err)
				continue
			}
			seen++
			es, ok := catalog[format]
			if !ok {
				t.Errorf("%s: no Spanish for %q", path, format)
				continue
			}
			if len(verbs(format)) != len(verbs(es)) {
				t.Errorf("%q has %d values, its Spanish %q has %d", format, len(verbs(format)), es, len(verbs(es)))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen < 200 {
		t.Fatalf("found only %d M formats; is the source walk broken?", seen)
	}
}

func TestTranslate(t *testing.T) {
	for _, c := range []struct{ en, es string }{
		{"released #12", "publicada la n.º 12"},
		{`rendimiento.yaml at c3a2ed8: services[0]: host "stockfinancia.com" is used more than once`,
			`rendimiento.yaml en c3a2ed8: services[0]: el host "stockfinancia.com" se usa más de una vez`},
		// A rollback: a list of failures inside a sentence, a percent sign.
		{"web (public URL): 3 of 10 checks failed (HTTP 502); it was 100% up in the hour before; rolled back to release #4 (as #6)",
			"web (URL pública): fallaron 3 de 10 comprobaciones (HTTP 502); estuvo activo el 100% de la hora anterior; se revirtió a la versión n.º 4 (como n.º 6)"},
		// Values reordered in Spanish, and translated themselves.
		{"post-deploy task smoke failed: exit status 1", "tarea smoke (después del despliegue): fallido: exit status 1"},
		{"web (public URL) and api (inside the cluster) answered normally again.",
			"Volvió a responder con normalidad: web (URL pública) y api (dentro del clúster)."},
		{"Ready to deploy; 1 optional item to review", "Listo para desplegar; 1 elemento opcional por revisar"},
		{"Nodes", "Nodos"},
		{"something nobody translated", "something nobody translated"},
		{"", ""},
	} {
		if got := Translate(Spanish, c.en); got != c.es {
			t.Errorf("Translate(%q)\n got %q\nwant %q", c.en, got, c.es)
		}
		if got := Translate(English, c.en); got != c.en {
			t.Errorf("English changed %q to %q", c.en, got)
		}
	}
	if TranslateExact(Spanish, "Nodes") != "Nodos" || TranslateExact(Spanish, "web (public URL)") != "web (public URL)" {
		t.Error("TranslateExact must only translate whole catalog entries")
	}
}

func TestParse(t *testing.T) {
	for in, want := range map[string]Lang{"es": Spanish, "es-MX,es;q=0.9": Spanish, "en-US": English, "": English, "fr": English} {
		if got := Parse(in); got != want {
			t.Errorf("Parse(%q) = %q, want %q", in, got, want)
		}
	}
}
