// Package web embeds the dashboard (no build step).
package web

import "embed"

//go:embed index.html app.js editor.js theme.css stats.html stats.js stats.css fonts
var FS embed.FS
