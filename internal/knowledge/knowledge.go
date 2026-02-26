// Package knowledge provides embedded platform knowledge for LLM prompt construction.
package knowledge

import (
	"fmt"
	"strings"
)

// Knowledge holds platform knowledge for building enhanced LLM system prompts.
type Knowledge struct {
	Base       string // core behavioral rules (embedded)
	Challenges string // challenge verification rules (embedded)
	Platform   string // platform quality standards (embedded)
	APIs       string // platform API reference (embedded)
	Soul       string // agent personality (from ~/.clawwork/soul.md, may be empty)

	// SpecVersion tracks the last seen server spec version for change detection.
	SpecVersion string
	SpecHash    string
}

// Load returns knowledge loaded from embedded docs and the user's encrypted soul file.
func Load(apiKey string) (*Knowledge, error) {
	soul, err := LoadSoul(apiKey)
	if err != nil {
		return nil, fmt.Errorf("load soul: %w", err)
	}
	return &Knowledge{
		Base:       strings.TrimSpace(baseDoc),
		Challenges: strings.TrimSpace(challengesDoc),
		Platform:   strings.TrimSpace(platformDoc),
		APIs:       strings.TrimSpace(apisDoc),
		Soul:       strings.TrimSpace(soul),
	}, nil
}

// SystemPrompt builds the full system prompt from all knowledge layers.
// Structure: base rules → personality (if set) → challenge rules → platform rules.
func (k *Knowledge) SystemPrompt() string {
	var parts []string

	parts = append(parts, k.Base)

	if k.Soul != "" {
		parts = append(parts, k.Soul)
	}

	parts = append(parts, k.Challenges)
	parts = append(parts, k.Platform)
	parts = append(parts, k.APIs)

	return strings.Join(parts, "\n\n")
}

// HasSoul returns true if the agent has a personality configured.
func (k *Knowledge) HasSoul() bool {
	return k.Soul != ""
}

// CheckSpecUpdate detects if the server's spec version has changed.
// Returns true and a message if an update is detected.
func (k *Knowledge) CheckSpecUpdate(version, hash string) (changed bool, msg string) {
	if version == "" {
		return false, ""
	}
	if k.SpecVersion == "" {
		// First time seeing a version — store it silently.
		k.SpecVersion = version
		k.SpecHash = hash
		return false, ""
	}
	if version != k.SpecVersion || hash != k.SpecHash {
		old := k.SpecVersion
		k.SpecVersion = version
		k.SpecHash = hash
		return true, fmt.Sprintf("Platform spec updated: %s -> %s", old, version)
	}
	return false, ""
}
