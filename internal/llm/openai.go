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

// OpenAIProvider implements Provider for any OpenAI-compatible API
// (OpenAI, Kimi, Groq, Together AI, vLLM, etc.).
type OpenAIProvider struct {
	baseURL      string
	apiKey       string
	model        string
	systemPrompt string
	maxTokens    int
	client       *http.Client
}

// NewOpenAI creates a new OpenAI-compatible provider.
func NewOpenAI(baseURL, apiKey, model, systemPrompt string, maxTokens int) *OpenAIProvider {
	return &OpenAIProvider{
		baseURL:      strings.TrimRight(baseURL, "/"),
		apiKey:       apiKey,
		model:        model,
		systemPrompt: systemPrompt,
		maxTokens:    maxTokens,
		client:       &http.Client{Timeout: 60 * time.Second},
	}
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	MaxTokens int          `json:"max_tokens,omitempty"`
}

type chatMessage struct {
	Role             string `json:"role"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content,omitempty"` // thinking models (Kimi, DeepSeek, etc.)
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *OpenAIProvider) Answer(ctx context.Context, prompt string) (string, error) {
	reqBody := chatRequest{
		Model: p.model,
		Messages: []chatMessage{
			{Role: "system", Content: p.systemPrompt},
			{Role: "user", Content: prompt},
		},
		MaxTokens: p.maxTokens,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	url := p.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

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
		return "", fmt.Errorf("LLM returned %d: %s", resp.StatusCode, truncateStr(string(respBody), 200))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if chatResp.Error != nil {
		return "", fmt.Errorf("LLM error: %s", chatResp.Error.Message)
	}
	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("LLM returned empty choices")
	}

	msg := chatResp.Choices[0].Message
	content := strings.TrimSpace(msg.Content)

	// Thinking models (Kimi K2.5, DeepSeek-R1, etc.) may put the answer
	// in reasoning_content instead of content (when max_tokens is exhausted
	// by reasoning). Extract just the last paragraph as the likely conclusion.
	if content == "" && msg.ReasoningContent != "" {
		content = extractConclusion(msg.ReasoningContent)
	}

	return content, nil
}

func (p *OpenAIProvider) Name() string {
	return fmt.Sprintf("openai-compat (%s)", p.model)
}

// extractConclusion pulls the last non-empty paragraph from a thinking model's
// reasoning chain, which is typically the final answer/conclusion.
func extractConclusion(reasoning string) string {
	reasoning = strings.TrimSpace(reasoning)
	// Split on double newlines (paragraph breaks).
	parts := strings.Split(reasoning, "\n\n")
	for i := len(parts) - 1; i >= 0; i-- {
		p := strings.TrimSpace(parts[i])
		if p != "" {
			return p
		}
	}
	return reasoning
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
