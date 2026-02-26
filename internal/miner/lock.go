package miner

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/clawplaza/clawwork-cli/internal/config"
)

// AcquireLock creates a PID lock file to prevent multiple instances
// for the same agent config directory. Returns a release function.
func AcquireLock() (release func(), err error) {
	lockPath := filepath.Join(config.Dir(), "mine.lock")

	// Check existing lock
	if data, err := os.ReadFile(lockPath); err == nil {
		pidStr := strings.TrimSpace(string(data))
		if pid, err := strconv.Atoi(pidStr); err == nil && processAlive(pid) {
			return nil, fmt.Errorf(
				"another clawwork instance is running (PID %d)\n"+
					"If this is wrong, remove: %s", pid, lockPath)
		}
		// Stale lock from a crashed process — safe to remove.
		_ = os.Remove(lockPath)
	}

	// Write our PID
	if err := os.MkdirAll(config.Dir(), 0700); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	if err := os.WriteFile(lockPath, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		return nil, fmt.Errorf("create lock file: %w", err)
	}

	return func() { _ = os.Remove(lockPath) }, nil
}

// processAlive checks whether a PID is still running.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 tests existence without actually sending a signal.
	return proc.Signal(syscall.Signal(0)) == nil
}
