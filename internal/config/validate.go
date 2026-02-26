package config

import (
	"fmt"
	"strings"
)

// Validate checks that the config has all required fields.
func (c *Config) Validate() error {
	if c.Agent.APIKey == "" {
		return fmt.Errorf("agent.api_key is required — run 'clawwork init'")
	}
	if !strings.HasPrefix(c.Agent.APIKey, "clwk_") || len(c.Agent.APIKey) != 69 {
		return fmt.Errorf("agent.api_key format invalid (expected clwk_ + 64 hex chars)")
	}
	if c.Agent.TokenID < 25 || c.Agent.TokenID > 1024 {
		return fmt.Errorf("agent.token_id must be between 25 and 1024")
	}

	switch c.LLM.Provider {
	case "platform":
		if c.LLM.APIKey == "" {
			return fmt.Errorf("llm.api_key is required for platform mode (plat_ key)")
		}
	case "openai", "anthropic":
		if c.LLM.APIKey == "" {
			return fmt.Errorf("llm.api_key is required for provider %q", c.LLM.Provider)
		}
		if c.LLM.Model == "" {
			return fmt.Errorf("llm.model is required")
		}
	case "ollama":
		if c.LLM.Model == "" {
			return fmt.Errorf("llm.model is required")
		}
	default:
		return fmt.Errorf("llm.provider must be one of: platform, openai, anthropic, ollama")
	}
	return nil
}

// Redact returns a copy of the config with API keys masked for display.
func (c *Config) Redact() *Config {
	copy := *c
	copy.Agent.APIKey = redactKey(c.Agent.APIKey)
	copy.LLM.APIKey = redactKey(c.LLM.APIKey)
	return &copy
}

func redactKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}
