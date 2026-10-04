//go:build e2e && (linux || darwin)

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type harness struct {
	t               *testing.T
	bin, data, port string
	env             []string
}

func setup(t *testing.T) *harness {
	dir := t.TempDir()
	bin := filepath.Join(dir, "trk")
	if out, err := exec.Command("go", "build", "-o", bin, "../cmd/trk").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	l.Close()
	h := &harness{t: t, bin: bin, data: filepath.Join(dir, "data"), port: port}
	h.env = append(os.Environ(), "TRK_PORT="+port, "TRK_DATA_DIR="+h.data, "TRK_URL=", "TRK_SESSION=", "TRK_NO_SPAWN=")
	t.Cleanup(h.killDaemon)
	return h
}

func (h *harness) run(stdin string, args ...string) (int, string, time.Duration) {
	cmd := exec.Command(h.bin, args...)
	cmd.Env, cmd.Stdin = h.env, strings.NewReader(stdin)
	start := time.Now()
	out, _ := cmd.Output()
	return cmd.ProcessState.ExitCode(), string(out), time.Since(start)
}

// fakeClaudeSrc is a tiny program that runs `sh -c <script>` as a child. Built as
// a binary named "claude" it stands in for Claude Code in the process tree.
// (Copying /bin/sh doesn't work on macOS: it's a shim that re-execs bash.)
const fakeClaudeSrc = `package main

import (
	"os"
	"os/exec"
)

func main() {
	cmd := exec.Command("sh", "-c", os.Args[1])
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if cmd.Run() != nil {
		os.Exit(1)
	}
}
`

func (h *harness) fakeClaude() string {
	dir := filepath.Join(filepath.Dir(h.bin), "fakeclaude")
	fake := filepath.Join(dir, "claude")
	if _, err := os.Stat(fake); err == nil {
		return fake
	}
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(fakeClaudeSrc), 0o644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fakeclaude\n\ngo 1.21\n"), 0o644)
	build := exec.Command("go", "build", "-o", fake, ".")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		h.t.Fatalf("build fake claude: %v\n%s", err, out)
	}
	return fake
}

// underClaude runs a shell script whose ancestor process is named "claude".
func (h *harness) underClaude(name, script string) {
	dir := filepath.Join(filepath.Dir(h.bin), name)
	os.MkdirAll(dir, 0o755)
	cmd := exec.Command(h.fakeClaude(), script)
	cmd.Env, cmd.Dir = h.env, dir
	if out, err := cmd.CombinedOutput(); err != nil {
		h.t.Fatalf("fake claude %s: %v %s", name, err, out)
	}
}

func (h *harness) view() map[string]any {
	resp, err := http.Get("http://127.0.0.1:" + h.port + "/v1/sessions")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var v map[string]any
	json.NewDecoder(resp.Body).Decode(&v)
	return v
}

func (h *harness) session(id string) map[string]any {
	v := h.view()
	if v == nil {
		return nil
	}
	for _, s := range v["sessions"].([]any) {
		if m := s.(map[string]any); m["session_id"] == id {
			return m
		}
	}
	return nil
}

func (h *harness) waitFor(what string, within time.Duration, ok func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	h.t.Fatalf("timed out after %v waiting for %s", within, what)
}

func (h *harness) killDaemon() {
	b, err := os.ReadFile(filepath.Join(h.data, "trk.pid"))
	if err != nil {
		return
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	syscall.Kill(pid, syscall.SIGTERM)
	h.waitFor("daemon exit", 5*time.Second, func() bool { return syscall.Kill(pid, 0) != nil })
}

func hookJSON(sid, event, cwd, extra string) string {
	return fmt.Sprintf(`{"session_id":%q,"hook_event_name":%q,"cwd":%q%s}`, sid, event, cwd, extra)
}

func TestAcceptance(t *testing.T) {
	h := setup(t)

	// 1. Auto-spawn: no `trk serve`, a hook call brings the daemon up.
	code, out, _ := h.run(hookJSON("A", "SessionStart", "/w/a", ""), "hook")
	if code != 0 || out != "" {
		t.Fatalf("hook: code=%d out=%q", code, out)
	}
	h.waitFor("session A", 3*time.Second, func() bool { return h.session("A") != nil })

	// 2. Permission prompt → waiting within 1 s.
	start := time.Now()
	h.run(hookJSON("A", "PermissionRequest", "/w/a", `,"tool_name":"Bash","tool_input":{"command":"rm -rf build"}`), "hook")
	h.waitFor("waiting", time.Second, func() bool { s := h.session("A"); return s != nil && s["status"] == "waiting" })
	t.Logf("permission → waiting in %v", time.Since(start))

	// 3. Two concurrent fake Claude sessions; CLI calls attributed by process tree.
	h.underClaude("wt-one", fmt.Sprintf(`echo '%s' | %s hook && %s step "one step"`, hookJSON("ONE", "SessionStart", "/w/shared", ""), h.bin, h.bin))
	h.underClaude("wt-two", fmt.Sprintf(`echo '%s' | %s hook && %s step "two step"`, hookJSON("TWO", "SessionStart", "/w/shared", ""), h.bin, h.bin))
	h.waitFor("ptree attribution", 2*time.Second, func() bool {
		one, two := h.session("ONE"), h.session("TWO")
		return one != nil && two != nil && one["step_text"] == "one step" && two["step_text"] == "two step"
	})

	// 3b. Restart between two sessions in the same folder: a CLI call from ONE's
	// process must still land on ONE (pid map persisted), not on the newer TWO.
	h.underClaude("wt-one-again", fmt.Sprintf(`echo '%s' | %s hook && sleep 0.3 && kill $(cat %s/trk.pid) && sleep 1 && %s step "after restart"`,
		hookJSON("ONE", "PreToolUse", "/w/shared", `,"tool_name":"Bash","tool_input":{"command":"ls"}`), h.bin, h.data, h.bin))
	h.waitFor("attribution kept across restart", 3*time.Second, func() bool {
		one, two := h.session("ONE"), h.session("TWO")
		return one != nil && two != nil && one["step_text"] == "after restart" && two["step_text"] == "two step"
	})

	// 4. Kill the daemon: calls stay fast and silent, next one respawns.
	h.killDaemon()
	t.Setenv("unused", "")
	code, out, took := h.run("", "step", "after kill")
	if code != 0 || out != "" || took > 2*time.Second {
		t.Fatalf("after kill: code=%d out=%q took=%v", code, out, took)
	}
	h.waitFor("respawn", 3*time.Second, func() bool { return h.view() != nil })

	// 5. Status line with null fields.
	code, out, took = h.run(`{"session_id":"A","context_window":{"used_percentage":null}}`, "statusline")
	if code != 0 || strings.TrimSpace(out) != "trk · ctx — · 5h — · 7d —" || took > time.Second {
		t.Fatalf("statusline: code=%d out=%q took=%v", code, out, took)
	}

	// 6. TRK_URL pointing nowhere: no spawn, exit 0, quick.
	cmd := exec.Command(h.bin, "step", "x")
	cmd.Env = append(h.env, "TRK_URL=http://127.0.0.1:1")
	t0 := time.Now()
	if err := cmd.Run(); err != nil || time.Since(t0) > 500*time.Millisecond {
		t.Fatalf("TRK_URL down: err=%v took=%v", err, time.Since(t0))
	}
	_ = io.Discard
}
