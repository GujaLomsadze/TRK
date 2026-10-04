package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/GujaLomsadze/trk/internal/claudecfg"
	"github.com/GujaLomsadze/trk/internal/client"
	"github.com/GujaLomsadze/trk/internal/config"
	"golang.org/x/term"
)

func initCmd(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	yes := fs.Bool("yes", false, "accept defaults without prompting (chains an existing status line)")
	noStatus := fs.Bool("no-statusline", false, "leave the status line alone")
	noMD := fs.Bool("no-claude-md", false, "don't add the CLAUDE.md block")
	mdPath := fs.String("claude-md", "", "CLAUDE.md to edit (default: <claude config dir>/CLAUDE.md)")
	dry := fs.Bool("dry-run", false, "show what would change, write nothing")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	paths, err := claudecfg.DefaultPaths()
	if err != nil {
		fmt.Fprintf(stderr, "trk init: %v\n", err)
		return 1
	}
	if *mdPath != "" {
		paths.ClaudeMD = *mdPath
	}
	in := bufio.NewReader(stdin)
	chain := func(existing string) bool {
		if *yes {
			return true
		}
		if !isTTY(stdin) {
			fmt.Fprintf(stdout, "Kept your status line (%s). Re-run with --yes to chain it through trk.\n", existing)
			return false
		}
		fmt.Fprintf(stdout, "You already have a status line: %s\nChain it through trk? trk records the data, then shows your status line unchanged. [Y/n] ", existing)
		line, _ := in.ReadString('\n')
		a := strings.ToLower(strings.TrimSpace(line))
		return a == "" || a == "y" || a == "yes"
	}
	res, err := claudecfg.Init(claudecfg.InitOptions{
		Paths: paths, TrkCmd: trkCommand(), ChainStatus: chain,
		SkipStatusLine: *noStatus, SkipClaudeMD: *noMD, DryRun: *dry, Now: time.Now(),
	})
	if err != nil {
		fmt.Fprintf(stderr, "trk init: %v\n", err)
		return 1
	}
	prefix := ""
	if *dry {
		prefix = "(dry run) would have: "
	}
	if res.HooksChanged {
		fmt.Fprintf(stdout, "%sadded trk hooks to %s\n", prefix, paths.Settings)
	} else {
		fmt.Fprintln(stdout, "hooks already set up")
	}
	switch res.Status {
	case claudecfg.StatusAdded:
		fmt.Fprintf(stdout, "%sset the status line to trk\n", prefix)
	case claudecfg.StatusChained:
		fmt.Fprintf(stdout, "%schained your status line through trk\n", prefix)
	case claudecfg.StatusPresent:
		fmt.Fprintln(stdout, "status line already goes through trk")
	}
	if res.SettingsBackup != "" {
		fmt.Fprintf(stdout, "backup: %s\n", res.SettingsBackup)
	}
	if res.ClaudeMDChanged {
		fmt.Fprintf(stdout, "%sadded the progress-reporting block to %s\n", prefix, paths.ClaudeMD)
	}
	if res.ClaudeMDBackup != "" {
		fmt.Fprintf(stdout, "backup: %s\n", res.ClaudeMDBackup)
	}
	if *dry {
		return 0
	}
	client.Default().EnsureDaemon(3 * time.Second)
	fmt.Fprintf(stdout, "\nDashboard: http://localhost:%d\nRestart any running Claude Code sessions so they load the hooks.\n", config.Port())
	return 0
}

func isTTY(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// trkCommand returns the command hooks should run: an absolute, stable path.
func trkCommand() string {
	exe, err := os.Executable()
	if err != nil {
		return "trk"
	}
	if lp, err := exec.LookPath("trk"); err == nil {
		if a, e1 := os.Stat(lp); e1 == nil {
			if b, e2 := os.Stat(exe); e2 == nil && os.SameFile(a, b) {
				if abs, err := filepath.Abs(lp); err == nil {
					exe = abs
				}
			}
		}
	}
	exe = filepath.ToSlash(exe)
	if strings.ContainsAny(exe, " '\"") {
		return claudecfg.ShellQuote(exe)
	}
	return exe
}
