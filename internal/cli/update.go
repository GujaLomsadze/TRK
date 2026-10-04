package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/GujaLomsadze/trk/internal/client"
	"github.com/GujaLomsadze/trk/internal/config"
	"github.com/GujaLomsadze/trk/internal/update"
	"github.com/GujaLomsadze/trk/internal/version"
)

// staleDaemon: a daemon is running but from a different binary (e.g. after an update).
func staleDaemon(running, own string) bool { return running != "" && running != own }

// staleAdvice explains how to stop a daemon too old to restart itself (pre-0.1.2 has no /v1/shutdown).
func staleAdvice(u ui, running, own string) string {
	stop := "pkill -f 'trk serve'"
	if runtime.GOOS == "windows" {
		stop = "taskkill /IM trk.exe /F"
	}
	return fmt.Sprintf("  %s An older trk service (%s) is still running, so the dashboard shows the old version.\n    Stop it, then run trk open again (it starts %s):\n      %s\n",
		u.warn("!"), running, own, u.accent(stop))
}

func isSourceBuild() bool {
	_, ok := update.ParseVersion(version.Version)
	return !ok
}

func updater() *update.Updater {
	dir, _ := config.DataDir()
	return update.Default(dir)
}

// updateCmd: `trk update [--check]`.
func updateCmd(args []string, stdout, stderr io.Writer) int {
	u := newUI(stdout)
	check := len(args) > 0 && args[0] == "--check"
	if isSourceBuild() {
		fmt.Fprintf(stdout, "\n  %s  %s\n\n  This is a source build (%s). Update with:\n    %s\n\n",
			u.accent("TRK.EXE"), u.dim("update"), version.Version, u.accent("git pull && make install"))
		return 0
	}
	u.banner(stdout, "update")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	up := updater()
	latest, err := up.Latest(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "  %s couldn't check for updates: %v\n\n", u.warn("!"), err)
		return 1
	}
	if !update.Newer(version.Version, latest) {
		u.row(stdout, u.ok("✓"), "Up to date", "trk "+version.Version)
		fmt.Fprintln(stdout)
		return 0
	}
	if check {
		u.row(stdout, u.warn("↑"), "Available", fmt.Sprintf("%s → %s", version.Version, strings.TrimPrefix(latest, "v")))
		fmt.Fprintf(stdout, "\n  Run %s to install it.\n\n", u.accent("trk update"))
		return 0
	}
	return applyUpdate(stdout, stderr, u, up, latest)
}

func applyUpdate(stdout, stderr io.Writer, u ui, up *update.Updater, latest string) int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "  %s can't find the trk binary: %v\n\n", u.warn("✗"), err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := up.Apply(ctx, latest, exe); err != nil {
		fmt.Fprintf(stderr, "  %s update failed: %v\n\n", u.warn("✗"), err)
		return 1
	}
	u.row(stdout, u.ok("✓"), "Updated", fmt.Sprintf("%s → %s", version.Version, strings.TrimPrefix(latest, "v"))+u.dim(" (checksum verified)"))
	c := client.Default()
	if c.Healthy() {
		agents := c.DashboardAgents()
		if agents > 0 {
			u.row(stdout, u.warn("!"), "Agents", agentsGone(agents))
		}
		if c.Restart(3 * time.Second) {
			u.row(stdout, u.ok("✓"), "Daemon", "restarted on the new version")
		} else {
			u.row(stdout, u.warn("!"), "Daemon", "stopped"+u.dim(" — starts again on next use"))
		}
	}
	fmt.Fprintln(stdout)
	return 0
}

// offerUpdate runs before `trk open`: at most one GitHub check a day, quick timeout,
// silent on any failure. Asks before installing when there's a terminal.
func offerUpdate(stdin io.Reader, stdout, stderr io.Writer) {
	if isSourceBuild() || os.Getenv("TRK_NO_UPDATE_CHECK") != "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	up := updater()
	latest, err := up.CachedLatest(ctx)
	if err != nil || !update.Newer(version.Version, latest) {
		return
	}
	u := newUI(stdout)
	next := strings.TrimPrefix(latest, "v")
	if !isTTY(stdin) {
		fmt.Fprintf(stdout, "  %s trk %s is available (you have %s). Run %s\n", u.warn("↑"), next, version.Version, u.accent("trk update"))
		return
	}
	fmt.Fprintf(stdout, "\n  %s trk %s is available (you have %s). Update now? %s ", u.warn("↑"), u.bold(next), version.Version, u.dim("[Y/n]"))
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a != "" && a != "y" && a != "yes" {
		return
	}
	fmt.Fprintln(stdout)
	if applyUpdate(stdout, stderr, u, update.Default(""), latest) == 0 {
		fmt.Fprintf(stdout, "  %s\n\n", u.dim("The dashboard is now served by the new version."))
	}
}
