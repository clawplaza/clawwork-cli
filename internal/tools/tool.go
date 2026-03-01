// Package tools provides built-in tools that the agent can call during chat.
// It defines the Tool interface, shared types, and the agentic loop.
package tools

import "context"

// Tool is a callable function the agent can invoke.
type Tool interface {
	// Def returns the tool's definition (name, description, parameters schema).
	Def() ToolDef
	// Call executes the tool with JSON-encoded arguments and returns the result string.
	Call(ctx context.Context, argsJSON string) string
}

// ToolDef is the OpenAI-compatible tool definition passed to the LLM.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  ToolParameters `json:"parameters"`
}

// ToolParameters describes the JSON Schema for the tool's input.
type ToolParameters struct {
	Type       string                  `json:"type"`
	Properties map[string]ToolProperty `json:"properties"`
	Required   []string                `json:"required,omitempty"`
}

// ToolProperty describes a single parameter field.
type ToolProperty struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

// Message is a chat message that supports all roles including tool results.
type Message struct {
	Role       string     `json:"role"`                  // system, user, assistant, tool
	Content    string     `json:"content,omitempty"`     // text content
	ToolCallID string     `json:"tool_call_id,omitempty"` // for role=tool
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`  // for assistant with pending calls
}

// ToolCall is a tool invocation requested by the LLM.
type ToolCall struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	ArgsJSON string `json:"args_json"` // JSON-encoded arguments
}

// ChatToolProvider is an LLM provider that supports the tool-calling protocol.
// Implemented by OpenAIProvider (and any OpenAI-compatible provider).
// The provider automatically prepends its configured system prompt.
type ChatToolProvider interface {
	// ChatWithTools sends messages and tool definitions to the LLM.
	// Returns (text_reply, tool_calls, finish_reason, error).
	// finish_reason is "tool_calls" when the LLM wants to invoke tools,
	// or "stop" when it has a final text reply.
	ChatWithTools(ctx context.Context, messages []Message, tools []ToolDef) (string, []ToolCall, string, error)
}

// Defaults returns all built-in tools available to the agent.
func Defaults() []Tool {
	return []Tool{
		NewShellExecTool(),   // shell: curl/wget/git/grep/jq/etc.
		NewHTTPFetchTool(),   // native HTTP GET/POST (no shell required)
		NewRunScriptTool(),   // execute Python or JavaScript
		NewFilesystemTool(),  // read/write/list/mkdir/move/delete/info
	}
}
