package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const anthropicURL = "https://api.anthropic.com/v1/messages"

// AnthropicProvider implements Provider for the Anthropic Messages API.
type AnthropicProvider struct {
	apiKey       string
	model        string
	systemPrompt string
	maxTokens    int
	client       *http.Client
}

// NewAnthropic creates a new Anthropic provider.
func NewAnthropic(apiKey, model, systemPrompt string, maxTokens int) *AnthropicProvider {
	return &AnthropicProvider{
		apiKey:       apiKey,
		model:        model,
		systemPrompt: systemPrompt,
		maxTokens:    maxTokens,
		client:       &http.Client{Timeout: 60 * time.Second},
	}
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *AnthropicProvider) Answer(ctx context.Context, prompt string) (string, error) {
	reqBody := anthropicRequest{
		Model:     p.model,
		MaxTokens: p.maxTokens,
		System:    p.systemPrompt,
		Messages: []anthropicMessage{
			{Role: "user", Content: prompt},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", anthropicURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Anthropic returned %d: %s", resp.StatusCode, truncateStr(string(respBody), 200))
	}

	var anthropicResp anthropicResponse
	if err := json.Unmarshal(respBody, &anthropicResp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if anthropicResp.Error != nil {
		return "", fmt.Errorf("Anthropic error: %s", anthropicResp.Error.Message)
	}
	if len(anthropicResp.Content) == 0 {
		return "", fmt.Errorf("Anthropic returned empty content")
	}

	return strings.TrimSpace(anthropicResp.Content[0].Text), nil
}

func (p *AnthropicProvider) Name() string {
	return fmt.Sprintf("anthropic (%s)", p.model)
}
