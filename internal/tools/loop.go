package tools

import (
	"context"
	"fmt"
	"strings"
)

const maxToolRounds = 6 // max LLM→tool→LLM cycles per Chat() call

// ToolUse records a single tool invocation during the agent loop.
type ToolUse struct {
	Name    string // tool name, e.g. "shell_exec"
	Summary string // first 80 chars of the result, for display
}

// RunAgentLoop drives the multi-turn tool-calling loop for a single user message.
//
// Flow:
//  1. Call LLM with messages + tool definitions.
//  2. If finish_reason == "tool_calls": execute each requested tool, append results, loop.
//  3. If finish_reason == "stop": return the final text reply.
//
// Returns the final reply and a list of tool invocations that occurred (may be empty).
// The provider automatically prepends its system prompt; callers should NOT include
// a system message in messages.
func RunAgentLoop(
	ctx context.Context,
	provider ChatToolProvider,
	messages []Message,
	tools []Tool,
) (string, []ToolUse, error) {
	// Build tool definitions and a name→Tool lookup map.
	toolMap := make(map[string]Tool, len(tools))
	toolDefs := make([]ToolDef, len(tools))
	for i, t := range tools {
		def := t.Def()
		toolMap[def.Name] = t
		toolDefs[i] = def
	}

	// Work on a copy of messages so the caller's slice is not modified.
	msgs := make([]Message, len(messages))
	copy(msgs, messages)

	var used []ToolUse

	for round := 0; round < maxToolRounds; round++ {
		content, reasoningContent, toolCalls, finishReason, err := provider.ChatWithTools(ctx, msgs, toolDefs)
		if err != nil {
			return "", used, err
		}

		// LLM has a final answer — return it.
		if finishReason != "tool_calls" || len(toolCalls) == 0 {
			return content, used, nil
		}

		// Append the assistant's "I want to call these tools" message.
		// Include content and reasoning_content from the response so thinking
		// models (Kimi, DeepSeek-R1) can verify the chain on the next turn.
		msgs = append(msgs, Message{
			Role:             "assistant",
			Content:          content,
			ReasoningContent: reasoningContent,
			ToolCalls:        toolCalls,
		})

		// Execute each requested tool and append the results.
		for _, call := range toolCalls {
			result := dispatchTool(ctx, toolMap, call)
			used = append(used, ToolUse{Name: call.Name, Summary: truncate80(result)})
			msgs = append(msgs, Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    result,
			})
		}
	}

	return "", used, fmt.Errorf("agent loop exceeded %d tool-call rounds", maxToolRounds)
}

func truncate80(s string) string {
	// Strip newlines for single-line summary.
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}

// dispatchTool executes a single tool call.
func dispatchTool(ctx context.Context, toolMap map[string]Tool, call ToolCall) string {
	t, ok := toolMap[call.Name]
	if !ok {
		return fmt.Sprintf("error: unknown tool %q", call.Name)
	}
	return t.Call(ctx, call.ArgsJSON)
}
