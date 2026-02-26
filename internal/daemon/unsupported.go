//go:build !darwin && !linux

package daemon

import "fmt"

// New returns an error on unsupported platforms.
func New() (Manager, error) {
	return nil, fmt.Errorf("background service not supported on this platform — use 'clawwork insc' to run in foreground")
}
