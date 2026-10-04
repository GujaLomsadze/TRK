// Package client sends events to the daemon. It never returns errors to callers:
// TRK must not break an agent.
package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/GujaLomsadze/trk/internal/config"
	"github.com/GujaLomsadze/trk/internal/model"
)

const Timeout = 200 * time.Millisecond

type Client struct {
	BaseURL     string
	HTTP        *http.Client
	AllowSpawn  bool
	Spawn       func() error
	SpawnBudget time.Duration
}

func Default() *Client {
	return &Client{
		BaseURL:     config.BaseURL(),
		HTTP:        &http.Client{Timeout: Timeout},
		AllowSpawn:  config.IsLocalDefault() && os.Getenv("TRK_NO_SPAWN") == "",
		Spawn:       SpawnDaemon,
		SpawnBudget: time.Second,
	}
}

func (c *Client) Send(env model.Envelope) bool {
	body, err := json.Marshal(env)
	if err != nil {
		return false
	}
	err = c.post(body)
	if err == nil {
		return true
	}
	if !c.AllowSpawn || c.Spawn == nil || !isDial(err) || c.Spawn() != nil {
		return false
	}
	for deadline := time.Now().Add(c.SpawnBudget); time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
		if c.post(body) == nil {
			return true
		}
	}
	return false
}

func (c *Client) post(body []byte) error {
	resp, err := c.HTTP.Post(c.BaseURL+"/v1/events", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) Healthy() bool {
	resp, err := c.HTTP.Get(c.BaseURL + "/healthz")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// DaemonVersion is the running daemon's version, or "" if none answers.
func (c *Client) DaemonVersion() string {
	resp, err := c.HTTP.Get(c.BaseURL + "/healthz")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var h struct {
		App     string `json:"app"`
		Version string `json:"version"`
	}
	if json.NewDecoder(resp.Body).Decode(&h) != nil || h.App != "trk" {
		return ""
	}
	return h.Version
}

// Shutdown asks the daemon to exit (the next trk call starts a fresh one).
func (c *Client) Shutdown() bool {
	resp, err := c.HTTP.Post(c.BaseURL+"/v1/shutdown", "application/json", nil)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusAccepted
}

// Restart stops a running daemon and starts one from the current binary.
func (c *Client) Restart(wait time.Duration) bool {
	if c.Shutdown() {
		for deadline := time.Now().Add(wait); time.Now().Before(deadline) && c.Healthy(); {
			time.Sleep(100 * time.Millisecond)
		}
	}
	return c.EnsureDaemon(wait)
}

// EnsureDaemon is for interactive commands (open/init) that may wait longer.
func (c *Client) EnsureDaemon(wait time.Duration) bool {
	if c.Healthy() {
		return true
	}
	if !c.AllowSpawn || c.Spawn == nil || c.Spawn() != nil {
		return false
	}
	for deadline := time.Now().Add(wait); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if c.Healthy() {
			return true
		}
	}
	return false
}

func isDial(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

func SpawnDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "serve")
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
