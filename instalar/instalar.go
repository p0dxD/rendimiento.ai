// Package instalar is the installer for one computer, which rendimiento
// serves at /instalar so that `curl -fsSL https://rendimiento.ai/instalar | sh`
// installs it.
package instalar

import _ "embed"

// Script is instalar.sh.
//
//go:embed instalar.sh
var Script []byte
