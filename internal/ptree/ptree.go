// Package ptree walks parent processes to find the Claude Code process a command runs under.
package ptree

import (
	"path/filepath"
	"strconv"
	"strings"
)

type proc struct {
	PPID    int
	Name    string
	Cmdline string
	Zombie  bool // exited, not yet reaped by its parent
}

func FindClaude(start int) int {
	if start <= 1 {
		return 0
	}
	return findClaude(start, snapshot())
}

func findClaude(pid int, lookup func(int) (proc, bool)) int {
	for depth := 0; pid > 1 && depth < 64; depth++ {
		p, ok := lookup(pid)
		if !ok {
			return 0
		}
		if isClaude(p) {
			return pid
		}
		if p.PPID == pid {
			return 0
		}
		pid = p.PPID
	}
	return 0
}

func isClaude(p proc) bool {
	name := strings.ToLower(filepath.Base(strings.ReplaceAll(p.Name, `\`, "/")))
	name = strings.TrimSuffix(name, ".exe")
	switch name {
	case "claude":
		return true
	case "node", "bun":
		c := strings.ToLower(p.Cmdline)
		return strings.Contains(c, "@anthropic-ai/claude-code") || strings.Contains(c, "/bin/claude")
	}
	return false
}

// parseStat parses /proc/<pid>/stat. The comm field may contain spaces and parens.
func parseStat(b []byte) (proc, bool) {
	s := string(b)
	l, r := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if l < 0 || r < l {
		return proc{}, false
	}
	f := strings.Fields(s[r+1:])
	if len(f) < 2 {
		return proc{}, false
	}
	ppid, err := strconv.Atoi(f[1])
	if err != nil {
		return proc{}, false
	}
	return proc{PPID: ppid, Name: s[l+1 : r], Zombie: f[0] == "Z"}, true
}

// Table answers several process questions against one snapshot of the process list.
type Table struct{ lookup func(int) (proc, bool) }

func Snapshot() Table { return Table{snapshot()} }

// IsClaude reports whether pid is a running Claude Code process.
func (t Table) IsClaude(pid int) bool {
	if pid <= 1 {
		return false
	}
	p, ok := t.lookup(pid)
	return ok && !p.Zombie && isClaude(p)
}

// Descends reports whether ancestor is pid itself or one of its parents.
func (t Table) Descends(pid, ancestor int) bool {
	for depth := 0; pid > 1 && depth < 64; depth++ {
		if pid == ancestor {
			return true
		}
		p, ok := t.lookup(pid)
		if !ok || p.PPID == pid {
			return false
		}
		pid = p.PPID
	}
	return false
}
