// Package cli implements every trk subcommand.
package cli

import (
	"fmt"
	"io"

	"github.com/GujaLomsadze/trk/internal/version"
)

const usageText = `trk — live tracker for coding agents

  trk serve                      run daemon + dashboard (auto-spawned when needed)
  trk open                       open the dashboard
  trk init                       wire up Claude Code hooks, status line, CLAUDE.md
  trk update [--check]           install the latest release (restarts the daemon)

  trk start "<task>" --steps N   declare a task
  trk step "<what>"              declare the current step
  trk progress i/N               declare progress
  trk blocked "<question>"       ask the human for a decision
  trk done "<result>"            declare completion

  trk hook                       (Claude Code) forward hook JSON from stdin
  trk statusline [--then CMD]    (Claude Code) forward status JSON, print a line
  trk version
`

func usage(w io.Writer) { fmt.Fprint(w, usageText) }

// Run executes one trk invocation and returns the process exit code.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stdout)
		return 0
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "start", "step", "progress", "blocked", "done", "hook", "statusline":
		return safe(func() int {
			switch cmd {
			case "hook":
				return hook(stdin)
			case "statusline":
				return statusline(rest, stdin, stdout)
			}
			return report(cmd, rest, stderr)
		})
	case "serve":
		return serve(stdout, stderr)
	case "open":
		return open(stdin, stdout, stderr)
	case "update":
		return updateCmd(rest, stdout, stderr)
	case "init":
		return initCmd(rest, stdin, stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "trk %s\n", version.Version)
		return 0
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	}
	fmt.Fprintf(stderr, "trk: unknown command %q (try `trk help`)\n", cmd)
	return 2
}

// safe guarantees exit 0 for agent-facing commands, even on panic.
func safe(f func() int) (code int) {
	defer func() {
		if recover() != nil {
			code = 0
		}
	}()
	f()
	return 0
}
