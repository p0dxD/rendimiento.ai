package mercado

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"strings"
)

//go:embed pagina.html.tmpl
var plantillas embed.FS

var plantilla = template.Must(template.New("pagina.html.tmpl").
	Funcs(template.FuncMap{"inc": func(i int) int { return i + 1 }}).
	ParseFS(plantillas, "pagina.html.tmpl"))

// Textos are the page's own labels, in its language, by kind of page.
type textos struct {
	Sobre, Galeria, Detalles, Pie, Foto string
}

var etiquetas = map[string]map[string]textos{
	"es": {
		TipoPersonal:   {"Sobre mí", "Fotos", "Datos", "Hecha a mano en el Mercado de rendimiento", "Foto"},
		TipoNegocio:    {"Quiénes somos", "Galería", "Visítenos", "Hecha a mano en el Mercado de rendimiento", "Foto"},
		TipoEvento:     {"El evento", "Fotos", "Cuándo y dónde", "Hecha a mano en el Mercado de rendimiento", "Foto"},
		TipoPortafolio: {"Sobre mi trabajo", "Mi trabajo", "Datos", "Hecha a mano en el Mercado de rendimiento", "Trabajo"},
	},
	"en": {
		TipoPersonal:   {"About me", "Photos", "Details", "Handmade at the rendimiento Mercado", "Photo"},
		TipoNegocio:    {"About us", "Gallery", "Visit us", "Handmade at the rendimiento Mercado", "Photo"},
		TipoEvento:     {"The event", "Photos", "When and where", "Handmade at the rendimiento Mercado", "Photo"},
		TipoPortafolio: {"About my work", "My work", "Details", "Handmade at the rendimiento Mercado", "Work"},
	},
}

var enlaceNombres = map[string]map[string]string{
	"es": {"instagram": "Instagram", "facebook": "Facebook", "tiktok": "TikTok", "whatsapp": "WhatsApp", "correo": "Correo", "telefono": "Teléfono", "web": "Sitio web"},
	"en": {"instagram": "Instagram", "facebook": "Facebook", "tiktok": "TikTok", "whatsapp": "WhatsApp", "correo": "Email", "telefono": "Phone", "web": "Website"},
}

type enlaceVista struct {
	Tipo, Nombre, Texto string
	Href                template.URL
}

type vista struct {
	P        *Pagina
	Lang     string
	Colores  template.CSS
	Textos   textos
	Parrafos []string
	Foto     template.URL
	Galeria  []template.URL
	Enlaces  []enlaceVista
	Banderas []bandera
}

// bandera is one flag of the papel picado across the top of the page.
type bandera struct {
	Color string
	Forma string  // the flag, with its zigzag edge
	Ojos  float64 // where its cut-outs are
}

var (
	fotoRepo = regexp.MustCompile(`^fotos/[0-9a-f]{12}\.(jpg|png|webp|gif)$`)
	fotoData = regexp.MustCompile(`^data:(image/(?:jpeg|png|webp|gif));base64,([A-Za-z0-9+/]+=*)$`)
)

// fotoURL is where the page loads a photo from: a data: URL as it came
// from the form (the preview), or a path of the repository, relative to
// base ("" on the published page, the site's address in a preview).
func fotoURL(f, base string) (template.URL, error) {
	switch {
	case fotoData.MatchString(f):
		return template.URL(f), nil
	case fotoRepo.MatchString(f):
		return template.URL(base + f), nil
	}
	return "", errors.New(M("a photo must be a JPEG, PNG, WebP or GIF image"))
}

// Render turns a page into its HTML. base is where photos of the repository
// are, for a preview (e.g. "https://ana.example.com/"); "" when publishing.
func Render(p *Pagina, base string) ([]byte, error) {
	pal, ok := Paletas[p.Paleta]
	if !ok {
		pal = Paletas["cempasuchil"]
	}
	lang := p.Idioma
	if lang != "en" {
		lang = "es"
	}
	tipo := p.Tipo
	if _, ok := etiquetas[lang][tipo]; !ok {
		tipo = TipoPersonal
	}
	v := vista{
		P: p, Lang: map[string]string{"es": "es-MX", "en": "en"}[lang], Textos: etiquetas[lang][tipo],
		Colores: template.CSS(fmt.Sprintf("--principal: %s; --segundo: %s; --oscuro: %s;", pal.Principal, pal.Segundo, pal.Oscuro)),
	}
	for _, par := range strings.Split(p.Sobre, "\n\n") {
		if par = strings.TrimSpace(par); par != "" {
			v.Parrafos = append(v.Parrafos, par)
		}
	}
	if p.Foto != "" {
		u, err := fotoURL(p.Foto, base)
		if err != nil {
			return nil, err
		}
		v.Foto = u
	}
	for _, f := range p.Galeria {
		u, err := fotoURL(f, base)
		if err != nil {
			return nil, err
		}
		v.Galeria = append(v.Galeria, u)
	}
	for _, e := range p.Enlaces {
		href, err := e.Href()
		if err != nil {
			return nil, err
		}
		texto := strings.TrimSpace(e.Valor)
		switch e.Tipo {
		case "instagram", "tiktok":
			texto = "@" + strings.TrimPrefix(lastSegment(texto), "@")
		case "facebook":
			texto = lastSegment(texto)
		case "web":
			texto = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(texto, "https://"), "http://"), "/")
		}
		v.Enlaces = append(v.Enlaces, enlaceVista{Tipo: e.Tipo, Nombre: enlaceNombres[lang][e.Tipo], Texto: texto, Href: template.URL(href)})
	}
	v.Banderas = banderas(pal)
	var b bytes.Buffer
	if err := plantilla.Execute(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// lastSegment is what follows the last / (a profile name in a link).
func lastSegment(s string) string {
	s = strings.TrimRight(s, "/")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// banderas strings the papel picado: fourteen flags in the page's colors
// and the rest of the Cotija palette.
func banderas(pal Paleta) []bandera {
	colores := []string{pal.Principal, "#D6246E", pal.Segundo, "#23408E", "#3E7B3A", "#E8890C", "#8E1B2C"}
	var out []bandera
	for i := 0; i < 14; i++ {
		x := float64(i)*100 + 8
		var d strings.Builder
		fmt.Fprintf(&d, "M%.0f 10 H%.0f V64", x, x+84)
		for t := 0; t < 6; t++ { // the cut edge, right to left
			r := x + 84 - float64(t)*14
			fmt.Fprintf(&d, " L%.0f 76 L%.0f 64", r-7, r-14)
		}
		d.WriteString(" Z")
		out = append(out, bandera{Color: colores[i%len(colores)], Forma: d.String(), Ojos: x + 42})
	}
	return out
}

// Fotos takes the photos just added in the form (data: URLs) out of the
// page: it returns them as files of the repository, named by their content,
// and leaves their paths in the page.
func Fotos(p *Pagina) (map[string][]byte, error) {
	files := map[string][]byte{}
	take := func(f string) (string, error) {
		if fotoRepo.MatchString(f) {
			return f, nil
		}
		m := fotoData.FindStringSubmatch(f)
		if m == nil {
			return "", errors.New(M("a photo must be a JPEG, PNG, WebP or GIF image"))
		}
		raw, err := base64.StdEncoding.DecodeString(m[2])
		if err != nil {
			return "", errors.New(M("a photo must be a JPEG, PNG, WebP or GIF image"))
		}
		if len(raw) > maxFoto {
			return "", errors.New(M("a photo is larger than %d MB", maxFoto>>20))
		}
		// Trust the bytes, not the label the browser put on them.
		ext := map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp", "image/gif": "gif"}[http.DetectContentType(raw)]
		if ext == "" {
			return "", errors.New(M("a photo must be a JPEG, PNG, WebP or GIF image"))
		}
		sum := sha256.Sum256(raw)
		name := "fotos/" + hex.EncodeToString(sum[:6]) + "." + ext
		files[name] = raw
		return name, nil
	}
	if p.Foto != "" {
		f, err := take(p.Foto)
		if err != nil {
			return nil, err
		}
		p.Foto = f
	}
	for i, g := range p.Galeria {
		f, err := take(g)
		if err != nil {
			return nil, err
		}
		p.Galeria[i] = f
	}
	return files, nil
}

// maxFoto is the largest photo a page takes (the form shrinks them first).
const maxFoto = 4 << 20

// fotosDe lists the repository photos a page uses.
func fotosDe(p *Pagina) []string {
	var out []string
	for _, f := range append([]string{p.Foto}, p.Galeria...) {
		if fotoRepo.MatchString(f) {
			out = append(out, f)
		}
	}
	return out
}
