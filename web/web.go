// Package web embeds the HTML pages so the binary is self-contained.
package web

import "embed"

//go:embed templates/*.html
var Templates embed.FS
