package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/GujaLomsadze/trk/internal/client"
	"github.com/GujaLomsadze/trk/internal/config"
)

// stopCmd pauses TRK and shuts the daemon down. While paused, hooks and agent
// calls skip silently, so nothing brings the daemon back until `trk open`.
func stopCmd(stdout, stderr io.Writer) int {
	u := newUI(stdout)
	if err := config.SetPaused(true); err != nil {
		fmt.Fprintf(stderr, "trk stop: %v\n", err)
		return 1
	}
	c := client.Default()
	running := c.Healthy()
	agents := 0
	if running {
		agents = c.DashboardAgents()
		c.Shutdown()
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline) && c.Healthy(); {
			time.Sleep(100 * time.Millisecond)
		}
	}
	u.banner(stdout, "stopped")
	if running {
		u.row(stdout, u.ok("✓"), "Service", "stopped")
		if agents > 0 {
			u.row(stdout, u.warn("!"), "Agents", agentsGone(agents))
		}
	} else {
		u.row(stdout, u.dim("·"), "Service", u.dim("wasn't running"))
	}
	u.row(stdout, u.ok("✓"), "Hooks", "paused"+u.dim(" — Claude keeps working normally, nothing is recorded"))
	fmt.Fprintf(stdout, "\n  Start again with %s\n\n", u.accent("trk open"))
	return 0
}

func agentsGone(n int) string {
	if n == 1 {
		return "1 agent started from the dashboard was stopped with it; resume it from its card"
	}
	return fmt.Sprintf("%d agents started from the dashboard were stopped with it; resume them from their cards", n)
}

// resume clears the pause so the daemon can run and agents report again.
func resume() { _ = config.SetPaused(false) }
