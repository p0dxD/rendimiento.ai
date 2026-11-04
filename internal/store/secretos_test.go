package store

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

// Sealed secrets are kept by name, replaced in place and removed.
func TestSecretos(t *testing.T) {
	s, ctx := open(t), context.Background()
	if _, err := s.LeerSecreto(ctx, "github-app"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("none yet: %v", err)
	}
	if err := s.GuardarSecreto(ctx, "github-app", Secreto{Nonce: []byte{1}, Cifrado: []byte{2, 3}}); err != nil {
		t.Fatal(err)
	}
	if err := s.GuardarSecreto(ctx, "github-app", Secreto{Nonce: []byte{4}, Cifrado: []byte{5}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.LeerSecreto(ctx, "github-app")
	if err != nil || !bytes.Equal(got.Nonce, []byte{4}) || !bytes.Equal(got.Cifrado, []byte{5}) {
		t.Fatalf("replaced: %+v %v", got, err)
	}
	if err := s.BorrarSecreto(ctx, "github-app"); err != nil {
		t.Fatal(err)
	}
	if err := s.BorrarSecreto(ctx, "github-app"); err != nil {
		t.Fatalf("removing it again: %v", err)
	}
	if _, err := s.LeerSecreto(ctx, "github-app"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after removing it: %v", err)
	}
}
