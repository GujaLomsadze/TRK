// Package config resolves TRK's environment-driven settings. Everything is optional.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const DefaultPort = 7777

func Port() int {
	if p, err := strconv.Atoi(os.Getenv("TRK_PORT")); err == nil && p > 0 && p < 65536 {
		return p
	}
	return DefaultPort
}

// BaseURL is where clients send events. TRK_URL wins (e.g. WSL ↔ Windows).
func BaseURL() string {
	if u := strings.TrimRight(os.Getenv("TRK_URL"), "/"); u != "" {
		return u
	}
	return fmt.Sprintf("http://127.0.0.1:%d", Port())
}

// IsLocalDefault reports whether clients talk to a daemon this machine may spawn.
func IsLocalDefault() bool { return os.Getenv("TRK_URL") == "" }

func DataDir() (string, error) {
	if d := os.Getenv("TRK_DATA_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "trk"), nil
	case "windows":
		if a := os.Getenv("APPDATA"); a != "" {
			return filepath.Join(a, "trk"), nil
		}
	}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "trk"), nil
	}
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "trk"), nil
}

// The pause flag is a file in the data dir. While it exists, every client call
// is a silent no-op and nothing auto-spawns the daemon (`trk stop` sets it,
// `trk open` / `trk serve` clear it).
func pausedFile() string {
	dir, err := DataDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "paused")
}

func Paused() bool {
	f := pausedFile()
	if f == "" {
		return false
	}
	_, err := os.Stat(f)
	return err == nil
}

func SetPaused(on bool) error {
	f := pausedFile()
	if f == "" {
		return fmt.Errorf("no data dir")
	}
	if !on {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		return err
	}
	return os.WriteFile(f, []byte("paused by trk stop\n"), 0o644)
}
