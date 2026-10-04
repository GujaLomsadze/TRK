//go:build windows

package term

import (
	"os"
	"os/exec"
)

func startPty(*exec.Cmd, uint16, uint16) (*os.File, error) { return nil, ErrUnsupported }

func resizePty(*os.File, uint16, uint16) error { return ErrUnsupported }

// Supported reports whether this build can run terminals.
const Supported = false
