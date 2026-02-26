//go:build darwin

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// New returns a macOS LaunchAgent service manager.
func New() (Manager, error) {
	return &launchdManager{}, nil
}

type launchdManager struct{}

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist")
}

func (m *launchdManager) Install() error {
	execPath, err := ExecPath()
	if err != nil {
		return err
	}

	logPath := LogPath()

	// Ensure log directory exists.
	if err := os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>insc</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
</dict>
</plist>
`, label, execPath, logPath, logPath)

	// Ensure LaunchAgents directory exists.
	if err := os.MkdirAll(filepath.Dir(plistPath()), 0755); err != nil {
		return fmt.Errorf("create LaunchAgents directory: %w", err)
	}

	if err := os.WriteFile(plistPath(), []byte(plist), 0644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}

	// Load and start the service.
	if out, err := exec.Command("launchctl", "load", "-w", plistPath()).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl load: %s (%w)", out, err)
	}

	return nil
}

func (m *launchdManager) Uninstall() error {
	pp := plistPath()
	if _, err := os.Stat(pp); os.IsNotExist(err) {
		return fmt.Errorf("service not installed")
	}

	// Unload (stops + disables).
	_ = exec.Command("launchctl", "unload", pp).Run()

	// Remove plist.
	if err := os.Remove(pp); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist: %w", err)
	}

	// Clean up log file.
	_ = os.Remove(LogPath())

	return nil
}

func (m *launchdManager) Start() error {
	if out, err := exec.Command("launchctl", "start", label).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl start: %s (%w)", out, err)
	}
	return nil
}

func (m *launchdManager) Stop() error {
	if out, err := exec.Command("launchctl", "stop", label).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl stop: %s (%w)", out, err)
	}
	return nil
}

func (m *launchdManager) Restart() error {
	_ = m.Stop()
	return m.Start()
}

func (m *launchdManager) Status() (*Status, error) {
	s := &Status{LogPath: LogPath()}

	// Check if plist exists (installed).
	if _, err := os.Stat(plistPath()); err == nil {
		s.Installed = true
	}

	// Check if process is running via lock file.
	if pid, alive := pidFromLockFile(); alive {
		s.Running = true
		s.PID = pid
	}

	return s, nil
}
