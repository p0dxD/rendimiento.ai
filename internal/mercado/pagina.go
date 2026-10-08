// Package mercado makes web pages for people who don't write code. A page
// (a puesto, a stall in the Mercado) is filled in a form; rendimiento
// renders it to static HTML, keeps it in a repository it creates (the form's
// answers in pagina.json, beside the HTML) and deploys it like any app.
// Everything here is plain text: nothing typed in the form becomes HTML.
package mercado

import (
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/p0dxD/rendimiento.ai/internal/i18n"
)

// Tipos of page; each names its sections and suggests its details.
const (
	TipoPersonal   = "personal"
	TipoNegocio    = "negocio"
	TipoEvento     = "evento"
	TipoPortafolio = "portafolio"
)

var tipos = []string{TipoPersonal, TipoNegocio, TipoEvento, TipoPortafolio}

// Paleta is a page's colors, from the Cotija palette: the awning, the
// headings and the buttons.
type Paleta struct {
	Principal, Segundo, Oscuro string
}

// Paletas a page can use, by name.
var Paletas = map[string]Paleta{
	"cempasuchil": {"#E8890C", "#F2C14E", "#7A3E0B"},
	"grana":       {"#8E1B2C", "#E9B824", "#4A0E18"},
	"anil":        {"#23408E", "#E8890C", "#121F4A"},
	"nopal":       {"#3E7B3A", "#E9B824", "#1F3D1D"},
	"rosa":        {"#D6246E", "#23408E", "#6B1E4A"},
}

// Enlace kinds: where people can find the page's owner.
var enlaceTipos = []string{"instagram", "facebook", "tiktok", "whatsapp", "correo", "telefono", "web"}

// Pagina is everything the form asks, saved as pagina.json.
type Pagina struct {
	Version int    `json:"version"`
	Tipo    string `json:"tipo"`
	Idioma  string `json:"idioma"` // es | en: the language of the page's own labels
	Nombre  string `json:"nombre"`
	Lema    string `json:"lema,omitempty"`
	Sobre   string `json:"sobre,omitempty"` // paragraphs, separated by blank lines
	Paleta  string `json:"paleta"`
	// Foto is the main photo and Galeria the others: paths in the
	// repository (fotos/…), or data: URLs of photos just added in the form.
	Foto     string    `json:"foto,omitempty"`
	Galeria  []string  `json:"galeria,omitempty"`
	Detalles []Detalle `json:"detalles,omitempty"`
	Enlaces  []Enlace  `json:"enlaces,omitempty"`
}

// Detalle is one labelled fact: Horario, Dirección, Fecha, Lugar…
type Detalle struct {
	Etiqueta string `json:"etiqueta"`
	Texto    string `json:"texto"`
}

// Enlace is a way to reach the owner: a username, a number, an email or an address.
type Enlace struct {
	Tipo  string `json:"tipo"`
	Valor string `json:"valor"`
}

const (
	maxFotos    = 9 // main photo and gallery together
	maxDetalles = 8
	maxEnlaces  = 8
)

// Default fills what the form may leave out.
func (p *Pagina) Default() {
	p.Version = 1
	if p.Tipo == "" {
		p.Tipo = TipoPersonal
	}
	if p.Idioma == "" {
		p.Idioma = "es"
	}
	if p.Paleta == "" {
		p.Paleta = "cempasuchil"
	}
	p.Nombre = strings.TrimSpace(p.Nombre)
	p.Lema = strings.TrimSpace(p.Lema)
	p.Sobre = strings.TrimSpace(strings.ReplaceAll(p.Sobre, "\r\n", "\n"))
	var ds []Detalle
	for _, d := range p.Detalles {
		d.Etiqueta, d.Texto = strings.TrimSpace(d.Etiqueta), strings.TrimSpace(d.Texto)
		if d.Etiqueta != "" || d.Texto != "" {
			ds = append(ds, d)
		}
	}
	p.Detalles = ds
	var es []Enlace
	for _, e := range p.Enlaces {
		if e.Valor = strings.TrimSpace(e.Valor); e.Valor != "" {
			es = append(es, e)
		}
	}
	p.Enlaces = es
}

// Validate checks a defaulted page. Photos are checked where they are read
// (see Fotos); here only how many there are.
func (p *Pagina) Validate() error {
	var errs []error
	long := func(v string, max int) bool { return utf8.RuneCountInString(v) > max }
	if !contains(tipos, p.Tipo) {
		errs = append(errs, errors.New(M("unknown kind of page %q", p.Tipo)))
	}
	if p.Idioma != "es" && p.Idioma != "en" {
		errs = append(errs, errors.New(M("the page's language must be es or en, not %q", p.Idioma)))
	}
	if _, ok := Paletas[p.Paleta]; !ok {
		errs = append(errs, errors.New(M("unknown palette %q", p.Paleta)))
	}
	if p.Nombre == "" {
		errs = append(errs, errors.New(M("the page needs a name")))
	}
	if long(p.Nombre, 80) {
		errs = append(errs, errors.New(M("the name is longer than %d characters", 80)))
	}
	if long(p.Lema, 160) {
		errs = append(errs, errors.New(M("the motto is longer than %d characters", 160)))
	}
	if long(p.Sobre, 4000) {
		errs = append(errs, errors.New(M("the text is longer than %d characters", 4000)))
	}
	if n := len(p.Galeria) + boolInt(p.Foto != ""); n > maxFotos {
		errs = append(errs, errors.New(M("a page holds up to %d photos, not %d", maxFotos, n)))
	}
	if len(p.Detalles) > maxDetalles {
		errs = append(errs, errors.New(M("a page holds up to %d details", maxDetalles)))
	}
	for _, d := range p.Detalles {
		if long(d.Etiqueta, 40) || long(d.Texto, 300) {
			errs = append(errs, errors.New(M("a detail is longer than %d characters (%d for its label)", 300, 40)))
		}
	}
	if len(p.Enlaces) > maxEnlaces {
		errs = append(errs, errors.New(M("a page holds up to %d links", maxEnlaces)))
	}
	for _, e := range p.Enlaces {
		if _, err := e.Href(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

var (
	usuario = regexp.MustCompile(`^@?([A-Za-z0-9._-]{1,60})/?$`)
	digitos = regexp.MustCompile(`\D`)
)

// Href is the link an Enlace becomes. Social networks take a username (or
// a link to the profile); only http(s), mailto: and tel: links come out.
func (e Enlace) Href() (string, error) {
	v := strings.TrimSpace(e.Valor)
	social := map[string]string{
		"instagram": "https://www.instagram.com/%s/",
		"facebook":  "https://www.facebook.com/%s",
		"tiktok":    "https://www.tiktok.com/@%s",
	}
	switch e.Tipo {
	case "instagram", "facebook", "tiktok":
		if u, err := url.Parse(v); err == nil && u.Host != "" {
			v = strings.Trim(u.Path, "/")
		}
		m := usuario.FindStringSubmatch(v)
		if m == nil {
			return "", errors.New(M("%q is not a %s username", e.Valor, e.Tipo))
		}
		return fmt.Sprintf(social[e.Tipo], m[1]), nil
	case "whatsapp", "telefono":
		d := digitos.ReplaceAllString(v, "")
		if len(d) < 7 || len(d) > 15 {
			return "", errors.New(M("%q is not a phone number", e.Valor))
		}
		if e.Tipo == "whatsapp" {
			return "https://wa.me/" + d, nil
		}
		return "tel:+" + d, nil
	case "correo":
		a, err := mail.ParseAddress(v)
		if err != nil || a.Name != "" || strings.ContainsAny(a.Address, "?&") {
			return "", errors.New(M("%q is not an email address", e.Valor))
		}
		return "mailto:" + a.Address, nil
	case "web":
		if !strings.Contains(v, "://") {
			v = "https://" + v
		}
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return "", errors.New(M("%q is not a web address", e.Valor))
		}
		return u.String(), nil
	}
	return "", errors.New(M("unknown kind of link %q", e.Tipo))
}

// M marks a message for translation (see internal/i18n).
var M = i18n.M

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
