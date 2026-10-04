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
