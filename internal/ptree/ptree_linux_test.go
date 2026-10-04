package ptree

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A copy of /bin/sh named "claude" stands in for Claude Code; we look up from its child.
func TestFindClaudeRealProcesses(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "claude")
	src, err := os.Open("/bin/sh")
	if err != nil {
		t.Skip("no /bin/sh")
	}
	dst, _ := os.OpenFile(fake, os.O_CREATE|os.O_WRONLY, 0o755)
	io.Copy(dst, src)
	src.Close()
	dst.Close()

	cmd := exec.Command(fake, "-c", "sleep 30; true") // "; true" stops sh from exec-ing sleep in place
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	var child int
	deadline := time.Now().Add(3 * time.Second)
	for child == 0 && time.Now().Before(deadline) {
		ents, _ := os.ReadDir("/proc")
		for _, e := range ents {
			var pid int
			if _, err := fmtSscan(e.Name(), &pid); err != nil {
				continue
			}
			if p, ok := readProc(pid); ok && p.PPID == cmd.Process.Pid {
				child = pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if child == 0 {
		t.Fatal("child of fake claude not found")
	}
	if got := FindClaude(child); got != cmd.Process.Pid {
		t.Fatalf("FindClaude(%d) = %d, want %d", child, got, cmd.Process.Pid)
	}
	if got := FindClaude(os.Getpid()); got != 0 && got == cmd.Process.Pid {
		t.Fatal("test process wrongly attributed to fake claude")
	}
}
func fmtSscan(s string, pid *int) (int, error) { return fmt.Sscan(s, pid) }
