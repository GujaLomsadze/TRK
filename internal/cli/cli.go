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
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "trk %s\n", version.Version)
		return 0
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	}
	fmt.Fprintf(stderr, "trk: unknown command %q (try `trk help`)\n", args[0])
	return 2
}
