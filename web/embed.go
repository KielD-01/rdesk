// Package web holds the browser viewer served by rdesk-server.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var static embed.FS

// FS is the viewer's static files, rooted at the site root.
var FS, _ = fs.Sub(static, "static")
