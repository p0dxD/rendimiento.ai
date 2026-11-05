package instalar

import (
	"bytes"
	"testing"
)

// The script served at /instalar is the POSIX shell installer.
func TestScript(t *testing.T) {
	if !bytes.HasPrefix(Script, []byte("#!/bin/sh\n")) || !bytes.Contains(Script, []byte("rendimiento-linux-$ARCH")) {
		t.Fatalf("not the installer: %.60q", Script)
	}
}
