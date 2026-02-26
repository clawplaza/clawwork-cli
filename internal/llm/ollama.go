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

// OllamaProvider implements Provider for a local Ollama instance.
type OllamaProvider struct {
	baseURL      string
	model        string
	systemPrompt string
	client       *http.Client
}

// NewOllama creates a new Ollama provider.
func NewOllama(baseURL, model, systemPrompt string) *OllamaProvider {
	return &OllamaProvider{
		baseURL:      strings.TrimRight(baseURL, "/"),
		model:        model,
		systemPrompt: systemPrompt,
		client:       &http.Client{Timeout: 60 * time.Second}, // local models can be slower
	}
}

type ollamaRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type ollamaResponse struct {
	Message chatMessage `json:"message"`
	Error   string      `json:"error,omitempty"`
}

func (p *OllamaProvider) Answer(ctx context.Context, prompt string) (string, error) {
	reqBody := ollamaRequest{
		Model: p.model,
		Messages: []chatMessage{
			{Role: "system", Content: p.systemPrompt},
			{Role: "user", Content: prompt},
		},
		Stream: false,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	url := p.baseURL + "/api/chat"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("request failed (is Ollama running?): %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Ollama returned %d: %s", resp.StatusCode, truncateStr(string(respBody), 200))
	}

	var ollamaResp ollamaResponse
	if err := json.Unmarshal(respBody, &ollamaResp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if ollamaResp.Error != "" {
		return "", fmt.Errorf("Ollama error: %s", ollamaResp.Error)
	}

	return strings.TrimSpace(ollamaResp.Message.Content), nil
}

func (p *OllamaProvider) Name() string {
	return fmt.Sprintf("ollama (%s)", p.model)
}
