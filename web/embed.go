// Package web embeds the dashboard (no build step).
package web

import "embed"

//go:embed index.html
var FS embed.FS
