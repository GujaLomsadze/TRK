package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/GujaLomsadze/trk/internal/claudecfg"
	"golang.org/x/term"
)

// ui styles interactive output. Colour only on a real terminal, never with NO_COLOR.
type ui struct{ color bool }

func newUI(w io.Writer) ui {
	f, ok := w.(*os.File)
	return ui{color: ok && term.IsTerminal(int(f.Fd())) && os.Getenv("NO_COLOR") == ""}
}

func (u ui) paint(code, s string) string {
	if !u.color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (u ui) accent(s string) string { return u.paint("1;38;2;255;90;74", s) } // phosphor red
func (u ui) ok(s string) string     { return u.paint("38;2;255;122;107", s) }
func (u ui) warn(s string) string   { return u.paint("38;2;255;178;122", s) } // amber
func (u ui) bold(s string) string   { return u.paint("1", s) }
func (u ui) dim(s string) string    { return u.paint("2", s) }

func (u ui) banner(w io.Writer, subtitle string) {
	fmt.Fprintf(w, "\n  %s  %s\n\n", u.accent("TRK.EXE"), u.dim(subtitle))
}

// row prints "  ✓ Label        detail".
func (u ui) row(w io.Writer, mark, label, detail string) {
	fmt.Fprintf(w, "  %s %s %s\n", mark, u.bold(fmt.Sprintf("%-12s", label)), detail)
}

func tilde(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return p
}

func renderInit(w io.Writer, u ui, r claudecfg.InitResult, p claudecfg.Paths, dry, daemonUp bool, port int) {
	done, same := u.ok("✓"), u.dim("·")
	verb := func(did, would string) string {
		if dry {
			return would
		}
		return did
	}
	if dry {
		u.banner(w, "dry run — nothing will be written")
	} else {
		u.banner(w, "connected to Claude Code")
	}

	if r.HooksChanged {
		u.row(w, done, "Hooks", verb("added to ", "would add to ")+tilde(p.Settings))
	} else {
		u.row(w, same, "Hooks", u.dim("already set up"))
	}
	switch r.Status {
	case claudecfg.StatusAdded:
		u.row(w, done, "Status line", verb("set to trk", "would set to trk"))
	case claudecfg.StatusChained:
		u.row(w, done, "Status line", verb("chained", "would chain")+u.dim(" — "+r.ExistingStatus+" still shows, unchanged"))
	case claudecfg.StatusPresent:
		u.row(w, same, "Status line", u.dim("already goes through trk"))
	case claudecfg.StatusKept:
		u.row(w, u.warn("!"), "Status line", "kept "+r.ExistingStatus+u.dim(" — re-run with --yes to chain it"))
	}
	if r.ClaudeMDChanged {
		u.row(w, done, "CLAUDE.md", verb("progress block added", "would add progress block")+u.dim(" — "+tilde(p.ClaudeMD)))
	} else if r.ClaudeMDBackup == "" {
		u.row(w, same, "CLAUDE.md", u.dim("already set up"))
	}
	if dry {
		if r.HooksChanged || r.ClaudeMDChanged || r.Status == claudecfg.StatusAdded || r.Status == claudecfg.StatusChained {
			fmt.Fprintf(w, "\n  Run %s to apply.\n\n", u.accent("trk init"))
		} else {
			fmt.Fprintf(w, "\n  %s\n\n", u.dim("Nothing to change."))
		}
		return
	}
	if daemonUp {
		u.row(w, done, "Daemon", "running")
	} else {
		u.row(w, u.warn("!"), "Daemon", "not started yet"+u.dim(" — starts on first use"))
	}

	if r.SettingsBackup != "" || r.ClaudeMDBackup != "" {
		fmt.Fprintf(w, "\n  %s\n", u.dim("Backups (restore these to undo)"))
		for _, b := range []string{r.SettingsBackup, r.ClaudeMDBackup} {
			if b != "" {
				fmt.Fprintf(w, "    %s\n", u.dim(tilde(b)))
			}
		}
	}

	fmt.Fprintf(w, "\n  %s\n", u.bold("Next"))
	fmt.Fprintf(w, "    %s  Restart your Claude Code sessions %s\n", u.accent("1"), u.dim("(hooks load at session start)"))
	fmt.Fprintf(w, "    %s  %s  %s\n\n", u.accent("2"), u.accent("trk open"), u.dim(fmt.Sprintf("→ http://localhost:%d", port)))
}
