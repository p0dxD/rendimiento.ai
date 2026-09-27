// Package templates embeds the Dockerfile templates used for repos that do not ship one.
package templates

import "embed"

//go:embed dockerfiles/*.tmpl
var Dockerfiles embed.FS
