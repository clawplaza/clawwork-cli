package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const platformURL = "https://platform-llm.eason9527.workers.dev"

// PlatformProvider calls the ClawWork platform LLM proxy.
// Users provide a platform key; the proxy handles the actual LLM call.
type PlatformProvider struct {
	apiKey string
	client *http.Client
}

// NewPlatform creates a new platform LLM provider.
func NewPlatform(apiKey string) *PlatformProvider {
	return &PlatformProvider{
		apiKey: apiKey,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

type platformRequest struct {
	Prompt string `json:"prompt"`
}

type platformResponse struct {
	Answer  string `json:"answer,omitempty"`
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}

func (p *PlatformProvider) Answer(ctx context.Context, prompt string) (string, error) {
	body, err := json.Marshal(platformRequest{Prompt: prompt})
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", platformURL+"/answer", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var result platformResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if resp.StatusCode != 200 || result.Error != "" {
		msg := result.Message
		if msg == "" {
			msg = result.Error
		}
		return "", fmt.Errorf("platform LLM error: %s", msg)
	}

	if result.Answer == "" {
		return "", fmt.Errorf("platform LLM returned empty answer")
	}

	return result.Answer, nil
}

func (p *PlatformProvider) Name() string {
	return "platform (kimi-k2.5)"
}
