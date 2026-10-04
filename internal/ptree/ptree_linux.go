package ptree

import (
	"fmt"
	"os"
	"strings"
)

func snapshot() func(int) (proc, bool) { return readProc }

func readProc(pid int) (proc, bool) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return proc{}, false
	}
	p, ok := parseStat(b)
	if !ok {
		return p, false
	}
	if p.Name == "node" || p.Name == "bun" {
		if c, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
			p.Cmdline = strings.ReplaceAll(string(c), "\x00", " ")
		}
	}
	return p, true
}
