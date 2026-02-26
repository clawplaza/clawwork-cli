// Package daemon manages ClawWork as a background service using
// platform-native service managers (launchd on macOS, systemd on Linux).
package daemon

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/clawplaza/clawwork-cli/internal/config"
)

// Manager defines platform-specific service management operations.
type Manager interface {
	Install() error
	Uninstall() error
	Start() error
	Stop() error
	Restart() error
	Status() (*Status, error)
}

// Status describes the current state of the background service.
type Status struct {
	Installed bool
	Running   bool
	PID       int
	LogPath   string
}

// LogPath returns the daemon log file path.
func LogPath() string {
	return filepath.Join(config.Dir(), "daemon.log")
}

// ExecPath returns the resolved absolute path of the running binary.
func ExecPath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot locate binary: %w", err)
	}
	p, err = filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("cannot resolve binary path: %w", err)
	}
	return p, nil
}

