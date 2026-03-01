package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/clawplaza/clawwork-cli/internal/tools"
)

// OpenAIProvider implements Provider for any OpenAI-compatible API
// (OpenAI, Kimi, Groq, Together AI, vLLM, etc.).
type OpenAIProvider struct {
	baseURL         string
	apiKey          string
	baseModel       string // original model from config (never changes)
	systemPrompt    string
	maxTokens       int
	client          *http.Client
	disableThinking atomic.Bool // when true, thinking mode is off
}

// NewOpenAI creates a new OpenAI-compatible provider.
func NewOpenAI(baseURL, apiKey, model, systemPrompt string, maxTokens int) *OpenAIProvider {
	return &OpenAIProvider{
		baseURL:      strings.TrimRight(baseURL, "/"),
		apiKey:       apiKey,
		baseModel:    model,
		systemPrompt: systemPrompt,
		maxTokens:    maxTokens,
		client:       &http.Client{Timeout: 120 * time.Second},
	}
}

// SetThinking implements llm.ThinkingToggler.
// Call with false to disable thinking mode (faster response, no reasoning chain).
func (p *OpenAIProvider) SetThinking(enabled bool) {
	p.disableThinking.Store(!enabled)
}

// activeModel returns the model to use for the current request.
// DeepSeek uses separate models for reasoning vs chat; other providers
// use the same model and control thinking via the enable_thinking flag.
func (p *OpenAIProvider) activeModel() string {
	if p.disableThinking.Load() && p.baseModel == "deepseek-reasoner" {
		return "deepseek-chat"
	}
	return p.baseModel
}

// thinkingField returns a *bool for the enable_thinking request field.
// Returns nil (field omitted) for DeepSeek (handled via model swap) and
// when thinking is enabled (API default). Returns &false only for other
// thinking models when the user disables thinking.
func (p *OpenAIProvider) thinkingField() *bool {
	if p.baseModel == "deepseek-reasoner" {
		return nil // DeepSeek: switch model instead, no flag needed
	}
	if p.disableThinking.Load() {
		v := false
		return &v
	}
	return nil
}

type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	MaxTokens      int           `json:"max_tokens,omitempty"`
	EnableThinking *bool         `json:"enable_thinking,omitempty"`
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
		Model: p.activeModel(),
		Messages: []chatMessage{
			{Role: "system", Content: p.systemPrompt},
			{Role: "user", Content: prompt},
		},
		MaxTokens:      p.maxTokens,
		EnableThinking: p.thinkingField(),
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
	return fmt.Sprintf("openai-compat (%s)", p.baseModel)
}

// ── Tool-calling support (OpenAI function-calling protocol) ──────────────────

// openToolCallFunc holds the name and JSON arguments of a tool call.
type openToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// openToolCall is an individual tool invocation returned by the LLM.
type openToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // always "function"
	Function openToolCallFunc `json:"function"`
}

// toolReqMessage is one message in a tool-aware chat request.
// Content is a pointer to allow JSON null (required when tool_calls is set).
type toolReqMessage struct {
	Role             string         `json:"role"`
	Content          *string        `json:"content"`                        // null when tool_calls present
	ReasoningContent string         `json:"reasoning_content,omitempty"`    // thinking tokens (Kimi, DeepSeek-R1)
	ToolCallID       string         `json:"tool_call_id,omitempty"`         // for role=tool
	ToolCalls        []openToolCall `json:"tool_calls,omitempty"`           // for role=assistant
}

// openFuncSpec is the function definition inside a tool spec.
type openFuncSpec struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"` // JSON Schema object
}

// openToolSpec is the full tool entry sent to the LLM.
type openToolSpec struct {
	Type     string       `json:"type"` // always "function"
	Function openFuncSpec `json:"function"`
}

// toolChatReq is the request body for a tool-aware chat completion.
type toolChatReq struct {
	Model          string           `json:"model"`
	Messages       []toolReqMessage `json:"messages"`
	MaxTokens      int              `json:"max_tokens,omitempty"`
	Tools          []openToolSpec   `json:"tools,omitempty"`
	ToolChoice     string           `json:"tool_choice,omitempty"`
	EnableThinking *bool            `json:"enable_thinking,omitempty"`
}

// toolChatResp is the response body for a tool-aware chat completion.
type toolChatResp struct {
	Choices []struct {
		Message struct {
			Content          *string        `json:"content"`
			ReasoningContent string         `json:"reasoning_content,omitempty"` // thinking models
			ToolCalls        []openToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// strPtr returns a pointer to s. Used to produce JSON string vs null for Content.
func strPtr(s string) *string { return &s }

// ChatWithTools implements tools.ChatToolProvider.
// It prepends the configured system prompt, converts messages to OpenAI format,
// and sends a single /chat/completions request with tool definitions.
func (p *OpenAIProvider) ChatWithTools(
	ctx context.Context,
	messages []tools.Message,
	toolDefs []tools.ToolDef,
) (string, string, []tools.ToolCall, string, error) {
	// Build OpenAI-format messages: system first, then caller messages.
	reqMsgs := make([]toolReqMessage, 0, len(messages)+1)
	if p.systemPrompt != "" {
		reqMsgs = append(reqMsgs, toolReqMessage{
			Role:    "system",
			Content: strPtr(p.systemPrompt),
		})
	}
	for _, m := range messages {
		rm := toolReqMessage{
			Role:             m.Role,
			ToolCallID:       m.ToolCallID,
			ReasoningContent: m.ReasoningContent, // echo back thinking tokens
		}
		if m.Content != "" {
			rm.Content = strPtr(m.Content)
		}
		for _, tc := range m.ToolCalls {
			rm.ToolCalls = append(rm.ToolCalls, openToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: openToolCallFunc{
					Name:      tc.Name,
					Arguments: tc.ArgsJSON,
				},
			})
		}
		reqMsgs = append(reqMsgs, rm)
	}

	// Build tool specs.
	specs := make([]openToolSpec, len(toolDefs))
	for i, td := range toolDefs {
		specs[i] = openToolSpec{
			Type: "function",
			Function: openFuncSpec{
				Name:        td.Name,
				Description: td.Description,
				Parameters:  td.Parameters,
			},
		}
	}

	req := toolChatReq{
		Model:          p.activeModel(),
		Messages:       reqMsgs,
		MaxTokens:      p.maxTokens,
		Tools:          specs,
		ToolChoice:     "auto",
		EnableThinking: p.thinkingField(),
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", "", nil, "", fmt.Errorf("marshal: %w", err)
	}

	url := p.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", "", nil, "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", "", nil, "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", nil, "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != 200 {
		return "", "", nil, "", fmt.Errorf("LLM returned %d: %s", resp.StatusCode, truncateStr(string(respBody), 200))
	}

	var chatResp toolChatResp
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", "", nil, "", fmt.Errorf("parse response: %w", err)
	}
	if chatResp.Error != nil {
		return "", "", nil, "", fmt.Errorf("LLM error: %s", chatResp.Error.Message)
	}
	if len(chatResp.Choices) == 0 {
		return "", "", nil, "", fmt.Errorf("LLM returned empty choices")
	}

	choice := chatResp.Choices[0]
	finishReason := choice.FinishReason
	reasoning := choice.Message.ReasoningContent

	// Tool calls requested — convert to tools.ToolCall slice.
	// Also capture content/reasoning_content so the caller can echo them back
	// in the assistant message (required by thinking models like Kimi).
	if finishReason == "tool_calls" && len(choice.Message.ToolCalls) > 0 {
		calls := make([]tools.ToolCall, len(choice.Message.ToolCalls))
		for i, tc := range choice.Message.ToolCalls {
			calls[i] = tools.ToolCall{
				ID:       tc.ID,
				Name:     tc.Function.Name,
				ArgsJSON: tc.Function.Arguments,
			}
		}
		msgContent := ""
		if choice.Message.Content != nil {
			msgContent = *choice.Message.Content
		}
		return msgContent, reasoning, calls, finishReason, nil
	}

	// Final text reply.
	content := ""
	if choice.Message.Content != nil {
		content = strings.TrimSpace(*choice.Message.Content)
	}
	return content, reasoning, nil, finishReason, nil
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
