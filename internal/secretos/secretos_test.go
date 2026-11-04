package secretos

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	gh "github.com/p0dxD/rendimiento.ai/internal/github"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

type memoria map[string]store.Secreto

func (m memoria) LeerSecreto(_ context.Context, n string) (store.Secreto, error) {
	s, ok := m[n]
	if !ok {
		return store.Secreto{}, store.ErrNotFound
	}
	return s, nil
}
func (m memoria) GuardarSecreto(_ context.Context, n string, s store.Secreto) error {
	m[n] = s
	return nil
}
func (m memoria) BorrarSecreto(_ context.Context, n string) error { delete(m, n); return nil }

func caja(t *testing.T, m memoria) *Caja {
	t.Helper()
	path := filepath.Join(t.TempDir(), "llave")
	if err := os.WriteFile(path, []byte(NuevaLlave()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	llave, err := LeerLlave(path)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Nueva(m, llave)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A value is kept sealed, opens only with its key and under its own name,
// and two seals of the same value differ.
func TestCaja(t *testing.T) {
	ctx := context.Background()
	m := memoria{}
	c := caja(t, m)
	if _, ok, err := c.Leer(ctx, "nada"); ok || err != nil {
		t.Fatalf("nothing kept: %v %v", ok, err)
	}
	if err := c.Guardar(ctx, "uno", []byte("valor secreto")); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(m["uno"].Cifrado, []byte("valor secreto")) {
		t.Fatal("the value is kept in the clear")
	}
	first := m["uno"]
	if v, ok, err := c.Leer(ctx, "uno"); err != nil || !ok || string(v) != "valor secreto" {
		t.Fatalf("open: %q %v %v", v, ok, err)
	}
	_ = c.Guardar(ctx, "uno", []byte("valor secreto"))
	if bytes.Equal(first.Nonce, m["uno"].Nonce) || bytes.Equal(first.Cifrado, m["uno"].Cifrado) {
		t.Fatal("two seals of one value are the same")
	}

	m["otro"] = m["uno"] // moved under another name
	if _, _, err := c.Leer(ctx, "otro"); !errors.Is(err, ErrNoAbre) {
		t.Fatalf("under another name: %v", err)
	}
	if _, _, err := caja(t, m).Leer(ctx, "uno"); !errors.Is(err, ErrNoAbre) {
		t.Fatalf("with another key: %v", err)
	}
	if _, err := Nueva(m, []byte("corta")); err == nil {
		t.Fatal("a short key was taken")
	}
	if err := c.Borrar(ctx, "uno"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := c.Leer(ctx, "uno"); ok {
		t.Fatal("still there after Borrar")
	}
}

// The GitHub App's credentials are kept once and never replaced; an
// organization's AI key shows only its end and an empty one removes it.
func TestUsos(t *testing.T) {
	ctx := context.Background()
	c := caja(t, memoria{})
	cr := Credenciales{Caja: c}
	if got, err := cr.Load(ctx); got != nil || err != nil {
		t.Fatalf("before setup: %v %v", got, err)
	}
	if err := cr.Save(ctx, &gh.Credentials{AppID: 7, ClientID: "cid"}); err != nil {
		t.Fatal(err)
	}
	if got, err := cr.Load(ctx); err != nil || got.AppID != 7 || got.ClientID != "cid" {
		t.Fatalf("load: %+v %v", got, err)
	}
	if err := cr.Save(ctx, &gh.Credentials{AppID: 8}); !errors.Is(err, ErrYaHay) {
		t.Fatalf("replacing them: %v", err)
	}

	k := LlavesIA{Caja: c}
	if err := k.SetOrgKey(ctx, 3, "sk-ant-0123456789abcd"); err != nil {
		t.Fatal(err)
	}
	if hint, _ := k.KeyHint(ctx, 3); hint != "…abcd" {
		t.Fatalf("hint: %q", hint)
	}
	if key, _ := k.OrgKey(ctx, 4); key != "" {
		t.Fatalf("another organization's key: %q", key)
	}
	_ = k.SetOrgKey(ctx, 3, "")
	if key, _ := k.OrgKey(ctx, 3); key != "" {
		t.Fatalf("after removing it: %q", key)
	}
}
