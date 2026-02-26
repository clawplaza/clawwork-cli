//go:build linux

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const unitName = "clawwork.service"

// New returns a Linux systemd user service manager.
func New() (Manager, error) {
	return &systemdManager{}, nil
}

type systemdManager struct{}

func unitPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user", unitName)
}

func (m *systemdManager) Install() error {
	execPath, err := ExecPath()
	if err != nil {
		return err
	}

	logPath := LogPath()

	// Ensure log directory exists.
	if err := os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}

	unit := fmt.Sprintf(`[Unit]
Description=ClawWork Inscription Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s insc
Restart=on-failure
RestartSec=30
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`, execPath, logPath, logPath)

	// Ensure systemd user directory exists.
	if err := os.MkdirAll(filepath.Dir(unitPath()), 0755); err != nil {
		return fmt.Errorf("create systemd directory: %w", err)
	}

	if err := os.WriteFile(unitPath(), []byte(unit), 0644); err != nil {
		return fmt.Errorf("write unit file: %w", err)
	}

	// Reload, enable, and start.
	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("daemon-reload: %s (%w)", out, err)
	}
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", "clawwork").CombinedOutput(); err != nil {
		return fmt.Errorf("enable service: %s (%w)", out, err)
	}

	return nil
}

func (m *systemdManager) Uninstall() error {
	if _, err := os.Stat(unitPath()); os.IsNotExist(err) {
		return fmt.Errorf("service not installed")
	}

	// Disable and stop.
	_ = exec.Command("systemctl", "--user", "disable", "--now", "clawwork").Run()

	// Remove unit file.
	if err := os.Remove(unitPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove unit file: %w", err)
	}

	// Reload daemon.
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()

	// Clean up log file.
	_ = os.Remove(LogPath())

	return nil
}

func (m *systemdManager) Start() error {
	if out, err := exec.Command("systemctl", "--user", "start", "clawwork").CombinedOutput(); err != nil {
		return fmt.Errorf("start service: %s (%w)", out, err)
	}
	return nil
}

func (m *systemdManager) Stop() error {
	if out, err := exec.Command("systemctl", "--user", "stop", "clawwork").CombinedOutput(); err != nil {
		return fmt.Errorf("stop service: %s (%w)", out, err)
	}
	return nil
}

func (m *systemdManager) Restart() error {
	if out, err := exec.Command("systemctl", "--user", "restart", "clawwork").CombinedOutput(); err != nil {
		return fmt.Errorf("restart service: %s (%w)", out, err)
	}
	return nil
}

func (m *systemdManager) Status() (*Status, error) {
	s := &Status{LogPath: LogPath()}

	// Check if unit file exists (installed).
	if _, err := os.Stat(unitPath()); err == nil {
		s.Installed = true
	}

	// Check if service is active.
	out, err := exec.Command("systemctl", "--user", "is-active", "clawwork").Output()
	if err == nil && strings.TrimSpace(string(out)) == "active" {
		s.Running = true
		// Try to get PID.
		pidOut, pidErr := exec.Command("systemctl", "--user", "show", "clawwork", "--property=MainPID", "--value").Output()
		if pidErr == nil {
			if pid, e := strconv.Atoi(strings.TrimSpace(string(pidOut))); e == nil && pid > 0 {
				s.PID = pid
			}
		}
	}

	return s, nil
}
