package mercado

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

// A 1×1 PNG.
var png = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 13, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89}

func dataURL(mime string, b []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
}

func TestEnlaces(t *testing.T) {
	for _, c := range []struct{ tipo, valor, want string }{
		{"instagram", "@pasteleria.ana", "https://www.instagram.com/pasteleria.ana/"},
		{"instagram", "https://www.instagram.com/pasteleria.ana/", "https://www.instagram.com/pasteleria.ana/"},
		{"tiktok", "ana_mx", "https://www.tiktok.com/@ana_mx"},
		{"facebook", "PasteleriaAna", "https://www.facebook.com/PasteleriaAna"},
		{"whatsapp", "+52 (55) 1234-5678", "https://wa.me/525512345678"},
		{"telefono", "55 1234 5678", "tel:+5512345678"},
		{"correo", "ana@example.com", "mailto:ana@example.com"},
		{"web", "example.com/menu", "https://example.com/menu"},
	} {
		got, err := Enlace{Tipo: c.tipo, Valor: c.valor}.Href()
		if err != nil || got != c.want {
			t.Errorf("%s %q = %q %v, want %q", c.tipo, c.valor, got, err, c.want)
		}
	}
	for _, e := range []Enlace{
		{"web", "javascript:alert(1)"},
		{"instagram", "ana\"><script>"},
		{"correo", "Ana <ana@example.com>"},
		{"correo", "ana@example.com?subject=hi"},
		{"whatsapp", "123"},
		{"fax", "123456789"},
	} {
		if h, err := e.Href(); err == nil {
			t.Errorf("%+v was accepted as %q", e, h)
		}
	}
}

func TestValidate(t *testing.T) {
	p := Pagina{Nombre: "  Pastelería Ana  ", Detalles: []Detalle{{" ", ""}, {"Horario", "9 a 18"}}, Enlaces: []Enlace{{"web", " "}}}
	p.Default()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if p.Nombre != "Pastelería Ana" || p.Tipo != TipoPersonal || p.Paleta != "cempasuchil" || p.Idioma != "es" || len(p.Detalles) != 1 || len(p.Enlaces) != 0 {
		t.Fatalf("defaults: %+v", p)
	}
	bad := Pagina{Tipo: "tienda", Paleta: "neón", Idioma: "fr", Lema: strings.Repeat("a", 161)}
	bad.Default()
	err := bad.Validate()
	for _, want := range []string{"unknown kind of page", "unknown palette", "language must be es or en", "needs a name", "motto is longer"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q in %v", want, err)
		}
	}
}

func TestRenderEscapes(t *testing.T) {
	p := Pagina{
		Tipo: TipoEvento, Idioma: "es", Paleta: "rosa",
		Nombre: `La boda <script>alert("x")</script>`,
		Sobre:  "Primer párrafo.\n\nSegundo <b>párrafo</b>.",
		Detalles: []Detalle{{"Fecha", "14 de febrero"}},
		Enlaces:  []Enlace{{"whatsapp", "5512345678"}, {"correo", "ana@example.com"}},
		Foto:     "fotos/0123456789ab.jpg",
		Galeria:  []string{dataURL("image/png", png)},
	}
	p.Default()
	html, err := Render(&p, "https://boda.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	for _, want := range []string{
		`lang="es-MX"`, "&lt;script&gt;", "<p>Primer párrafo.</p>", "Segundo &lt;b&gt;párrafo&lt;/b&gt;.",
		"Cuándo y dónde", "El evento", `href="https://wa.me/5512345678"`, `href="mailto:ana@example.com"`,
		`src="https://boda.example.com/fotos/0123456789ab.jpg"`, `src="data:image/png;base64,`, "--principal: #D6246E",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(s, "<script>") || strings.Contains(s, "ZgotmplZ") {
		t.Fatalf("unsafe or refused output:\n%s", s)
	}
	// An event says when and where before its story.
	if strings.Index(s, "Cuándo y dónde") > strings.Index(s, "El evento") {
		t.Error("an event's details come first")
	}
	p.Idioma, p.Tipo = "en", TipoNegocio
	html, _ = Render(&p, "")
	if s := string(html); !strings.Contains(s, "About us") || !strings.Contains(s, `src="fotos/0123456789ab.jpg"`) {
		t.Errorf("English business page: %s", s)
	}
	p.Foto = "../../etc/passwd"
	if _, err := Render(&p, ""); err == nil {
		t.Error("a photo outside fotos/ was rendered")
	}
}

func TestFotos(t *testing.T) {
	p := Pagina{Foto: dataURL("image/png", png), Galeria: []string{"fotos/0123456789ab.jpg", dataURL("image/png", png)}}
	files, err := Fotos(&p)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || !fotoRepo.MatchString(p.Foto) || p.Galeria[1] != p.Foto || p.Galeria[0] != "fotos/0123456789ab.jpg" {
		t.Fatalf("files %v, page %+v", files, p)
	}
	if string(files[p.Foto]) != string(png) {
		t.Fatal("photo bytes changed")
	}
	// The browser's label is not trusted: a page of text is not an image.
	if _, err := Fotos(&Pagina{Foto: dataURL("image/png", []byte("<html>hola</html>"))}); err == nil {
		t.Fatal("text was taken as a photo")
	}
	if _, err := Fotos(&Pagina{Foto: dataURL("image/png", make([]byte, maxFoto+1))}); err == nil {
		t.Fatal("an oversized photo was taken")
	}
}

func TestArchivos(t *testing.T) {
	p := Pagina{Nombre: "Ana", Foto: dataURL("image/png", png)}
	p.Default()
	fotos, err := Fotos(&p)
	if err != nil {
		t.Fatal(err)
	}
	sp := especificacion("ana.example.com")
	files, err := archivos(&p, fotos, &sp)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"index.html", Archivo, p.Foto, spec.FileName, "Dockerfile", ".dockerignore", "README.md"} {
		if _, ok := files[f]; !ok {
			t.Errorf("first commit lacks %s", f)
		}
	}
	got, err := spec.Parse(files[spec.FileName])
	if err != nil {
		t.Fatalf("the page's rendimiento.yaml: %v\n%s", err, files[spec.FileName])
	}
	if s := got.Services[0]; s.Domain != "ana.example.com" || s.Port != 8080 || s.Build.Dockerfile != "Dockerfile" {
		t.Errorf("service %+v", s)
	}
	var back Pagina
	if err := json.Unmarshal(files[Archivo], &back); err != nil || back.Foto != p.Foto || back.Nombre != "Ana" {
		t.Errorf("pagina.json %s: %v", files[Archivo], err)
	}
	// Later saves leave rendimiento.yaml to the repository.
	files, _ = archivos(&p, nil, nil)
	if _, ok := files[spec.FileName]; ok || len(files) != 2 {
		t.Errorf("a save writes only the page: %v", keys(files))
	}
}

func TestEnZona(t *testing.T) {
	zones := []string{"example.com", "rendimiento.example"}
	for d, want := range map[string]bool{
		"ana.example.com":         true,
		"pasteleria-ana.example.com": true,
		"example.com":             false,
		"a.b.example.com":         false,
		"ana.otro.com":            false,
		"-ana.example.com":        false,
		"1ana.example.com":        false,
	} {
		if got := enZona(d, zones); got != want {
			t.Errorf("enZona(%q) = %v", d, got)
		}
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
