package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/GujaLomsadze/trk/internal/claudecfg"
)

func TestIsTTYRejectsDevNull(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Skip(err)
	}
	defer f.Close()
	if isTTY(f) {
		t.Fatal("/dev/null treated as an interactive terminal")
	}
}

func TestRenderInitReport(t *testing.T) {
	home, _ := os.UserHomeDir()
	p := claudecfg.Paths{Settings: home + "/.claude/settings.json", ClaudeMD: home + "/.claude/CLAUDE.md"}
	r := claudecfg.InitResult{HooksChanged: true, Status: claudecfg.StatusChained, ExistingStatus: "ccstatusline",
		SettingsBackup: p.Settings + ".trk-backup-1", ClaudeMDChanged: true, ClaudeMDBackup: p.ClaudeMD + ".trk-backup-1"}
	var b bytes.Buffer
	renderInit(&b, ui{}, r, p, false, true, 7777)
	out := b.String()
	for _, want := range []string{"Hooks", "~/.claude/settings.json", "ccstatusline", "~/.claude/settings.json.trk-backup-1",
		"Next", "Restart", "trk open", "http://localhost:7777"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("ANSI codes with color off")
	}
	b.Reset()
	renderInit(&b, ui{}, r, p, true, false, 7777)
	if !strings.Contains(b.String(), "dry run") || strings.Contains(b.String(), "Restart") {
		t.Errorf("dry-run report:\n%s", b.String())
	}
}
