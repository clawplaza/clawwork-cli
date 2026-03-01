package tools

import (
	"context"
	"fmt"
)

const maxToolRounds = 6 // max LLM→tool→LLM cycles per Chat() call

// RunAgentLoop drives the multi-turn tool-calling loop for a single user message.
//
// Flow:
//  1. Call LLM with messages + tool definitions.
//  2. If finish_reason == "tool_calls": execute each requested tool, append results, loop.
//  3. If finish_reason == "stop": return the final text reply.
//
// The provider automatically prepends its system prompt; callers should NOT include
// a system message in messages.
func RunAgentLoop(
	ctx context.Context,
	provider ChatToolProvider,
	messages []Message,
	tools []Tool,
) (string, error) {
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

	for round := 0; round < maxToolRounds; round++ {
		content, toolCalls, finishReason, err := provider.ChatWithTools(ctx, msgs, toolDefs)
		if err != nil {
			return "", err
		}

		// LLM has a final answer — return it.
		if finishReason != "tool_calls" || len(toolCalls) == 0 {
			return content, nil
		}

		// Append the assistant's "I want to call these tools" message.
		msgs = append(msgs, Message{
			Role:      "assistant",
			ToolCalls: toolCalls,
		})

		// Execute each requested tool and append the results.
		for _, call := range toolCalls {
			result := dispatchTool(ctx, toolMap, call)
			msgs = append(msgs, Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    result,
			})
		}
	}

	return "", fmt.Errorf("agent loop exceeded %d tool-call rounds", maxToolRounds)
}

// dispatchTool executes a single tool call.
func dispatchTool(ctx context.Context, toolMap map[string]Tool, call ToolCall) string {
	t, ok := toolMap[call.Name]
	if !ok {
		return fmt.Sprintf("error: unknown tool %q", call.Name)
	}
	return t.Call(ctx, call.ArgsJSON)
}
