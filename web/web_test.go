package web

import (
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestAssetsEmbedded(t *testing.T) {
	for _, p := range []string{"index.html", "app.js", "editor.js", "theme.css", "limits.html", "limits.js", "limits.css", "stats.html", "term.html", "term.js", "agents.js", "vendor/xterm.js", "vendor/xterm.css", "vendor/addon-fit.js", "vendor/LICENSE-xterm.txt", "fonts/VT323-Regular.ttf", "fonts/IBMPlexMono-Regular.ttf", "fonts/IBMPlexMono-SemiBold.ttf"} {
		if _, err := fs.Stat(FS, p); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	idx, _ := fs.ReadFile(FS, "index.html")
	for _, ref := range []string{"theme.css", "app.js", "TRK.EXE", `href="limits.html"`} {
		if !strings.Contains(string(idx), ref) {
			t.Errorf("index.html lacks %s", ref)
		}
	}
	lim, _ := fs.ReadFile(FS, "limits.html")
	for _, ref := range []string{"theme.css", "limits.css", "limits.js", "TRK.EXE"} {
		if !strings.Contains(string(lim), ref) {
			t.Errorf("limits.html lacks %s", ref)
		}
	}
	old, _ := fs.ReadFile(FS, "stats.html") // old bookmarks
	if !strings.Contains(string(old), `url=limits.html`) {
		t.Error("stats.html does not forward to limits.html")
	}
}

// Agent-supplied text must never be parsed as HTML.
func TestNoInnerHTML(t *testing.T) {
	for _, f := range []string{"app.js", "editor.js", "limits.js", "term.js", "agents.js", "term.html"} {
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

// The out-of-the-box card layout (picked by the user, 2026-10-04; widgets added the same night;
// 2026-10-06: Files touched replaces Lines changed, Needs you and Collisions start hidden).
func TestDefaultLayout(t *testing.T) {
	js, _ := os.ReadFile("app.js")
	want := `Default: { order: ["task", "progress", "step", "reality", "prompt", "reply", "files", "busy", "subagents", "ctx", "tools", "model", "lines"], ` +
		`on: ["task", "progress", "step", "reality", "prompt", "reply", "files", "busy", "subagents", "ctx"], tools: 3, hints: true, reserve: false, ` +
		`footer: ["elapsed", "cost", "model", "last"] },`
	if !strings.Contains(string(js), want) {
		t.Fatalf("app.js Default preset is not the chosen default:\n%s", want)
	}
	if !strings.Contains(string(js), `const DEFAULT_AREAS = { needs: false, collisions: false, timeline: true, limits: true };`) {
		t.Fatal("Needs you and Collisions should start hidden")
	}
}

// "step i/N" sits to the right of the progress bar; the step line carries only the text.
func TestStepCountBesideBar(t *testing.T) {
	js, _ := os.ReadFile("app.js")
	for _, w := range []string{`class: "slot-bar prog-row"`, `class: "prog-count"`} {
		if !strings.Contains(string(js), w) {
			t.Errorf("app.js lacks %s", w)
		}
	}
}

// Each page holds one /v1/stream. A page parked in the back/forward cache keeps it open,
// and HTTP/1.1 allows 6 connections per host: after a few Fleet↔Limits trips the next page
// can't load. Both pages must close the stream on pagehide and reopen it on pageshow.
func TestStreamClosedOnPagehide(t *testing.T) {
	for _, f := range []string{"app.js", "limits.js"} {
		js, _ := os.ReadFile(f)
		for _, want := range []string{`addEventListener("pagehide"`, `addEventListener("pageshow"`, "es.close()"} {
			if !strings.Contains(string(js), want) {
				t.Errorf("%s lacks %s", f, want)
			}
		}
	}
}

// Card grid: columns (auto, 1–6) and rows on screen (auto, 1–4) are picked in the drawer.
func TestCardGridSetting(t *testing.T) {
	for f, wants := range map[string][]string{
		"app.js":    {"const GRID_COLS = 6, GRID_ROWS = 4;", "grid: { cols: 0, rows: 0 }", "function applyGrid(", "function fitRows("},
		"editor.js": {`"Card grid"`, `"data-grid"`, "p.grid = layout.grid"},
		"theme.css": {".grid.fixed-cols {", ".grid.fixed-rows {"},
	} {
		src, _ := os.ReadFile(f)
		for _, w := range wants {
			if !strings.Contains(string(src), w) {
				t.Errorf("%s lacks %s", f, w)
			}
		}
	}
}

// The five card widgets picked on 2026-10-04, each with a renderer and a drawer note.
func TestCardWidgets(t *testing.T) {
	app, _ := os.ReadFile("app.js")
	ed, _ := os.ReadFile("editor.js")
	for _, id := range []string{"prompt", "reply", "lines", "busy", "subagents"} {
		if !strings.Contains(string(app), "\n  "+id+": (s) =>") {
			t.Errorf("app.js has no renderer for %s", id)
		}
		if !regexp.MustCompile(`\b` + id + `: "`).Match(ed) {
			t.Errorf("editor.js has no note for %s", id)
		}
	}
}

// Terminals: off unless switched on in the layout drawer, xterm.js served locally (no CDN),
// the agent drawer never keeps more than the visible terminal connected, cards open it,
// and its width can be dragged.
func TestTerminalsWiring(t *testing.T) {
	idx, _ := os.ReadFile("index.html")
	for _, w := range []string{`id="agent-open"`, `hidden>+ Agent`, `src="vendor/xterm.js"`, `src="term.js"`, `src="agents.js"`, `id="agents"`} {
		if !strings.Contains(string(idx), w) {
			t.Errorf("index.html lacks %s", w)
		}
	}
	for _, f := range []string{"index.html", "term.html"} {
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), "cdn.jsdelivr") || strings.Contains(string(b), "unpkg.com") {
			t.Errorf("%s loads from a CDN", f)
		}
	}
	ed, _ := os.ReadFile("editor.js")
	if !strings.Contains(string(ed), `terminals_enabled: on`) {
		t.Error("layout drawer has no Terminals switch")
	}
	ag, _ := os.ReadFile("agents.js")
	if !strings.Contains(string(ag), "function dropViewer()") || !strings.Contains(string(ag), `dlg.addEventListener("close", dropViewer)`) {
		t.Error("agents.js must drop the viewer's websocket on switch and close")
	}
	if !strings.Contains(string(idx), `id="ag-grip"`) || !strings.Contains(string(ag), `grip.addEventListener("pointerdown"`) {
		t.Error("agent drawer is not resizable")
	}
	app, _ := os.ReadFile("app.js")
	if !strings.Contains(string(app), "TRKAgents.openSession(") {
		t.Error("cards do not open the agent drawer")
	}
}
