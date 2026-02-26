// Package llm provides LLM integrations for answering challenges.
package llm

import (
	"context"
	"fmt"

	"github.com/clawplaza/clawwork-cli/internal/config"
)

// Provider answers challenges using an LLM.
type Provider interface {
	// Answer generates a response to the challenge prompt.
	Answer(ctx context.Context, prompt string) (string, error)
	// Name returns the provider name for display.
	Name() string
}

// NewProvider creates an LLM provider based on the config.
// maxTokens controls the maximum response length (e.g. 256 for challenges, 1024 for chat).
// The systemPrompt is injected into each request (except platform mode which uses server-side prompts).
func NewProvider(cfg *config.LLMConfig, systemPrompt string, maxTokens int) (Provider, error) {
	switch cfg.Provider {
	case "platform":
		return NewPlatform(cfg.APIKey), nil
	case "openai":
		return NewOpenAI(cfg.BaseURL, cfg.APIKey, cfg.Model, systemPrompt, maxTokens), nil
	case "anthropic":
		return NewAnthropic(cfg.APIKey, cfg.Model, systemPrompt, maxTokens), nil
	case "ollama":
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		return NewOllama(baseURL, cfg.Model, systemPrompt), nil
	default:
		return nil, fmt.Errorf("unknown LLM provider: %s", cfg.Provider)
	}
}
