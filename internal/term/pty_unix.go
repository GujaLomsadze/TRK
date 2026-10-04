//go:build !windows

package term

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

func startPty(cmd *exec.Cmd, cols, rows uint16) (*os.File, error) {
	return pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
}

func resizePty(f *os.File, cols, rows uint16) error {
	return pty.Setsize(f, &pty.Winsize{Cols: cols, Rows: rows})
}

// Supported reports whether this build can run terminals.
const Supported = true
