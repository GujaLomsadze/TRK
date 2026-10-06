// Package web embeds the dashboard (no build step).
package web

import "embed"

//go:embed index.html app.js editor.js theme.css limits.html limits.js limits.css stats.html term.html term.js agents.js vendor fonts
var FS embed.FS
