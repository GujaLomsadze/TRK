// Package web embeds the dashboard (no build step).
package web

import "embed"

//go:embed index.html app.js editor.js theme.css fonts
var FS embed.FS
