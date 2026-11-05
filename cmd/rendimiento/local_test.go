package main

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// A port is kept: the wanted one when free, another free one when taken,
// and the same one afterwards either way.
func TestPuertoGuardado(t *testing.T) {
	dir := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	taken := ln.Addr().(*net.TCPAddr).Port
	got, err := puertoGuardado(filepath.Join(dir, "a"), taken)
	if err != nil || got == "" || got == strconv.Itoa(taken) {
		t.Fatalf("a taken port: %q %v", got, err)
	}
	if again, _ := puertoGuardado(filepath.Join(dir, "a"), taken); again != got {
		t.Fatalf("not kept: %q then %q", got, again)
	}
	free, _ := net.Listen("tcp", "127.0.0.1:0")
	want := free.Addr().(*net.TCPAddr).Port
	free.Close()
	if got, _ := puertoGuardado(filepath.Join(dir, "b"), want); got != strconv.Itoa(want) {
		t.Fatalf("a free port: %q, want %d", got, want)
	}
}

// The sbin folders join PATH once.
func TestSbin(t *testing.T) {
	old := os.Getenv("PATH")
	defer os.Setenv("PATH", old)
	sbin("/home/u/.local/bin:/usr/bin:/usr/sbin")
	if got := os.Getenv("PATH"); got != "/home/u/.local/bin:/usr/bin:/usr/sbin:/usr/local/sbin:/sbin" {
		t.Fatalf("PATH: %s", got)
	}
}
