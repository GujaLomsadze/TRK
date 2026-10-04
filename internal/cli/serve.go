package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/GujaLomsadze/trk/internal/client"
	"github.com/GujaLomsadze/trk/internal/config"
	"github.com/GujaLomsadze/trk/internal/daemon"
	"github.com/GujaLomsadze/trk/internal/version"
)

func serve(stdout, stderr io.Writer) int {
	dir, err := config.DataDir()
	if err == nil {
		err = os.MkdirAll(dir, 0o755)
	}
	if err != nil {
		fmt.Fprintf(stderr, "trk serve: data dir: %v\n", err)
		return 1
	}
	logPath := filepath.Join(dir, "trk.log")
	if st, err := os.Stat(logPath); err == nil && st.Size() > 5<<20 {
		os.Truncate(logPath, 0)
	}
	var logw io.Writer = io.Discard
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		defer f.Close()
		logw = f
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	addr := fmt.Sprintf("127.0.0.1:%d", config.Port())
	err = daemon.Run(ctx, daemon.Options{
		Addr: addr, DataDir: dir, Logger: log.New(logw, "", log.LstdFlags),
		OnListen: func(string) { fmt.Fprintf(stdout, "trk serving http://localhost:%d\n", config.Port()) },
	})
	if errors.Is(err, daemon.ErrAlreadyRunning) {
		fmt.Fprintf(stdout, "trk already running at http://localhost:%d\n", config.Port())
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "trk serve: %v\n", err)
		return 1
	}
	return 0
}

func open(stdin io.Reader, stdout, stderr io.Writer) int {
	url := config.BaseURL() + "/"
	offerUpdate(stdin, stdout, stderr)
	c := client.Default()
	if staleDaemon(c.DaemonVersion(), version.Version) {
		c.Restart(3 * time.Second) // daemon still running an older binary
		if running := c.DaemonVersion(); staleDaemon(running, version.Version) {
			fmt.Fprint(stderr, staleAdvice(newUI(stderr), running, version.Version))
		}
	}
	if !c.EnsureDaemon(3 * time.Second) {
		fmt.Fprintf(stderr, "trk: daemon not reachable at %s\n", url)
		return 1
	}
	fmt.Fprintln(stdout, url)
	_ = openBrowser(url)
	return 0
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	}
	if b, err := os.ReadFile("/proc/version"); err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft") {
		if p, err := exec.LookPath("wslview"); err == nil {
			return exec.Command(p, url).Start()
		}
		return exec.Command("cmd.exe", "/c", "start", "", url).Start()
	}
	return exec.Command("xdg-open", url).Start()
}
