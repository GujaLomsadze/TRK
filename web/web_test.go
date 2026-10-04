package web

import (
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestAssetsEmbedded(t *testing.T) {
	for _, p := range []string{"index.html", "app.js", "editor.js", "theme.css", "fonts/VT323-Regular.ttf", "fonts/IBMPlexMono-Regular.ttf", "fonts/IBMPlexMono-SemiBold.ttf"} {
		if _, err := fs.Stat(FS, p); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	idx, _ := fs.ReadFile(FS, "index.html")
	for _, ref := range []string{"theme.css", "app.js", "TRK.EXE"} {
		if !strings.Contains(string(idx), ref) {
			t.Errorf("index.html lacks %s", ref)
		}
	}
}

// Agent-supplied text must never be parsed as HTML.
func TestNoInnerHTML(t *testing.T) {
	for _, f := range []string{"app.js", "editor.js"} {
		js, _ := os.ReadFile(f)
		if regexp.MustCompile(`innerHTML|outerHTML|insertAdjacentHTML|document\.write`).Match(js) {
			t.Fatalf("%s uses an HTML-parsing sink", f)
		}
	}
}

func TestThemeTokens(t *testing.T) {
	css, _ := os.ReadFile("theme.css")
	for _, tok := range []string{"--bg: #0B0605", "--panel: #110908", "--border: #3A1A16", "--text: #F1DCD6", "--muted: #B9928A", "--accent: #FF5A4A", "--warn: #FFB27A", "--hot: #6E2A20",
		"--ok: #5CFF9D", "--yellow: #FFD66B", "--red: #FF4A3D", "--work: #F1DCD6"} {
		if !strings.Contains(string(css), tok) {
			t.Errorf("theme.css missing token %q", tok)
		}
	}
}

// Bars are drawn per cell so each cell can carry its own colour.
func TestPerCellBars(t *testing.T) {
	js, _ := os.ReadFile("app.js")
	for _, fn := range []string{"function cellBar(", "progCell", "ctxCell", "useCell"} {
		if !strings.Contains(string(js), fn) {
			t.Errorf("app.js lacks %s", fn)
		}
	}
}
