package web

import "embed"

//go:embed index.html
var EmbeddedFS embed.FS
