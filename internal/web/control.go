package web

import "sync"

// MinerControl provides thread-safe control over mining behavior.
// The miner loop reads IsPaused/TokenID; the web chat handler writes.
type MinerControl struct {
	mu      sync.RWMutex
	paused  bool
	tokenID int
}

// NewMinerControl creates a new control with the given initial token ID.
func NewMinerControl(tokenID int) *MinerControl {
	return &MinerControl{tokenID: tokenID}
}

// IsPaused returns whether mining is paused.
func (c *MinerControl) IsPaused() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.paused
}

// Pause pauses the mining loop.
func (c *MinerControl) Pause() {
	c.mu.Lock()
	c.paused = true
	c.mu.Unlock()
}

// Resume resumes the mining loop.
func (c *MinerControl) Resume() {
	c.mu.Lock()
	c.paused = false
	c.mu.Unlock()
}

// TokenID returns the current target token ID.
func (c *MinerControl) TokenID() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tokenID
}

// SetTokenID changes the target token ID (effective next inscription cycle).
func (c *MinerControl) SetTokenID(id int) {
	c.mu.Lock()
	c.tokenID = id
	c.mu.Unlock()
}
