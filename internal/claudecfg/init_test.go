package claudecfg

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func paths(t *testing.T) Paths {
	d := t.TempDir()
	return Paths{Settings: filepath.Join(d, "settings.json"), ClaudeMD: filepath.Join(d, "CLAUDE.md")}
}

func opts(p Paths, chain bool) InitOptions {
	return InitOptions{Paths: p, TrkCmd: trk, ChainStatus: func(string) bool { return chain }, Now: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)}
}

func backups(t *testing.T, p string) []string {
	m, _ := filepath.Glob(p + ".trk-backup-*")
	return m
}

func TestInitFreshMachine(t *testing.T) {
	p := paths(t)
	res, err := Init(opts(p, true))
	if err != nil || !res.HooksChanged || res.Status != StatusAdded || !res.ClaudeMDChanged || res.SettingsBackup != "" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	b, _ := os.ReadFile(p.Settings)
	md, _ := os.ReadFile(p.ClaudeMD)
	if !strings.Contains(string(b), trk+" hook") || string(md) != Snippet {
		t.Fatalf("settings=%s md=%q", b, md)
	}
}

func TestInitExistingThenIdempotent(t *testing.T) {
	p := paths(t)
	os.WriteFile(p.Settings, []byte(`{"model":"opus","statusLine":{"type":"command","command":"ccstatusline","refreshInterval":10}}`), 0o600)
	os.WriteFile(p.ClaudeMD, []byte("# Rules\n"), 0o644)
	res, err := Init(opts(p, true))
	if err != nil || res.Status != StatusChained || res.SettingsBackup == "" || res.ClaudeMDBackup == "" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	orig, _ := os.ReadFile(res.SettingsBackup)
	if !strings.Contains(string(orig), `"ccstatusline"`) || strings.Contains(string(orig), "trk") {
		t.Fatal("backup is not the original")
	}
	if st, _ := os.Stat(p.Settings); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("mode changed to %v", st.Mode().Perm())
	}
	b, _ := os.ReadFile(p.Settings)
	if strings.Index(string(b), `"model"`) > strings.Index(string(b), `"statusLine"`) {
		t.Fatal("key order changed")
	}
	n := len(backups(t, p.Settings))
	res2, err := Init(opts(p, true))
	if err != nil || res2.HooksChanged || res2.Status != StatusPresent || res2.ClaudeMDChanged || len(backups(t, p.Settings)) != n {
		t.Fatalf("second run not a no-op: %+v err=%v", res2, err)
	}
}

func TestInitInvalidJSONWritesNothing(t *testing.T) {
	p := paths(t)
	os.WriteFile(p.Settings, []byte(`{"hooks": {`), 0o644)
	if _, err := Init(opts(p, true)); err == nil {
		t.Fatal("accepted invalid settings.json")
	}
	b, _ := os.ReadFile(p.Settings)
	if string(b) != `{"hooks": {` || len(backups(t, p.Settings)) != 0 {
		t.Fatal("file touched")
	}
	if _, err := os.Stat(p.ClaudeMD); !os.IsNotExist(err) {
		t.Fatal("CLAUDE.md written despite settings error")
	}
}

func TestInitDryRun(t *testing.T) {
	p := paths(t)
	o := opts(p, true)
	o.DryRun = true
	res, err := Init(o)
	if err != nil || !res.HooksChanged {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if _, err := os.Stat(p.Settings); !os.IsNotExist(err) {
		t.Fatal("dry run wrote settings")
	}
}

func TestInitFollowsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges")
	}
	p := paths(t)
	target := filepath.Join(t.TempDir(), "dotfiles-settings.json")
	os.WriteFile(target, []byte(`{}`), 0o644)
	os.Symlink(target, p.Settings)
	if _, err := Init(opts(p, true)); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(p.Settings); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink replaced by a file")
	}
	b, _ := os.ReadFile(target)
	if !strings.Contains(string(b), "trk") {
		t.Fatal("target not updated")
	}
}
