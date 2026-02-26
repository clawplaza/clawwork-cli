// Package miner implements the core inscription loop.
package miner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/clawplaza/clawwork-cli/internal/api"
	"github.com/clawplaza/clawwork-cli/internal/config"
)

// State tracks inscription progress across restarts.
type State struct {
	LastChallenge     *api.Challenge `json:"last_challenge,omitempty"`
	TotalInscriptions int            `json:"total_inscriptions"`
	TotalCWEarned     int64          `json:"total_cw_earned"`
	TotalHits         int            `json:"total_hits"`
	ChallengesPassed  int            `json:"challenges_passed"`
	ChallengesFailed  int            `json:"challenges_failed"`
	LastTrustScore    int            `json:"last_trust_score,omitempty"`
	LastMineAt        time.Time      `json:"last_mine_at,omitempty"`
	path              string
}

// LoadState reads state from disk, returning a fresh state if not found.
func LoadState() *State {
	s := &State{path: filepath.Join(config.Dir(), "state.json")}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(data, s)
	return s
}

// Save persists the state to disk.
func (s *State) Save() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0600)
}

// Update updates the state from a successful inscription response.
func (s *State) Update(resp *api.InscribeResponse) {
	s.TotalInscriptions++
	s.TotalCWEarned += int64(resp.CWEarned)
	if resp.Hit {
		s.TotalHits++
	}
	s.ChallengesPassed++
	s.LastMineAt = time.Now()
	// Only overwrite if server provided a next challenge; preserve existing otherwise.
	if resp.NextChallenge != nil {
		s.LastChallenge = resp.NextChallenge
	}
}

// RecordChallengeFail increments the challenge failure counter.
func (s *State) RecordChallengeFail() {
	s.ChallengesFailed++
}
