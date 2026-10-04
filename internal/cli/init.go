package cli

import (
	"fmt"
	"io"
)

func initCmd(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "trk init: not implemented yet")
	return 1
}
