package cli

import (
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/GujaLomsadze/trk/internal/config"
)

func isolate(t *testing.T) {
	t.Helper()
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close() // nothing listens here: never touches a real daemon
	t.Setenv("TRK_PORT", strconv.Itoa(port))
	t.Setenv("TRK_URL", "")
	t.Setenv("TRK_NO_SPAWN", "1")
	t.Setenv("TRK_DATA_DIR", t.TempDir())
}

func TestStopPausesAndResumeClears(t *testing.T) {
	isolate(t)
	code, out, _ := run("stop")
	if code != 0 || !config.Paused() || !strings.Contains(out, "trk open") {
		t.Fatalf("stop: code=%d paused=%v out=%q", code, config.Paused(), out)
	}
	resume()
	if config.Paused() {
		t.Fatal("resume left TRK paused")
	}
}
