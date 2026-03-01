package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	scriptTimeout = 15 * time.Second
	maxOutputLen  = 8 * 1024 // 8 KB
)

// RunScriptTool executes a Python or JavaScript (Node.js) snippet.
// Requires python3 or node to be installed on the host machine.
// Falls back gracefully with a "not found" message if the runtime is absent.
type RunScriptTool struct{}

// NewRunScriptTool creates a new script execution tool.
func NewRunScriptTool() *RunScriptTool {
	return &RunScriptTool{}
}

func (t *RunScriptTool) Def() ToolDef {
	return ToolDef{
		Name:        "run_script",
		Description: "Execute a Python or JavaScript snippet locally. Use for data processing, calculations, or JSON transforms. Timeout 15s, max output 8KB.",
		Parameters: ToolParameters{
			Type: "object",
			Properties: map[string]ToolProperty{
				"language": {
					Type: "string",
					Enum: []string{"python", "javascript"},
				},
				"code": {
					Type:        "string",
					Description: "Code to execute (use print/console.log for output)",
				},
			},
			Required: []string{"language", "code"},
		},
	}
}

type runScriptArgs struct {
	Language string `json:"language"`
	Code     string `json:"code"`
}

func (t *RunScriptTool) Call(ctx context.Context, argsJSON string) string {
	var args runScriptArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("error: invalid arguments: %v", err)
	}

	ctx, cancel := context.WithTimeout(ctx, scriptTimeout)
	defer cancel()

	var cmd *exec.Cmd
	switch args.Language {
	case "python":
		cmd = exec.CommandContext(ctx, "python3", "-c", args.Code)
	case "javascript":
		cmd = exec.CommandContext(ctx, "node", "-e", args.Code)
	default:
		return fmt.Sprintf("error: unsupported language %q (use python or javascript)", args.Language)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Check if the binary is not found.
		if isNotFound(err, args.Language) {
			return runtimeNotFoundMsg(args.Language)
		}
		errOut := strings.TrimSpace(stderr.String())
		if errOut == "" {
			errOut = err.Error()
		}
		code := -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		return fmt.Sprintf("error (exit %d):\n%s", code, truncateOutput(errOut))
	}

	out := strings.TrimRight(stdout.String(), "\n")
	if out == "" {
		return "(no output)"
	}
	return truncateOutput(out)
}

func isNotFound(err error, lang string) bool {
	msg := err.Error()
	return strings.Contains(msg, "executable file not found") ||
		strings.Contains(msg, "no such file") ||
		strings.Contains(msg, "not found")
}

func runtimeNotFoundMsg(lang string) string {
	switch lang {
	case "python":
		return "error: python3 is not installed. Install it from https://python.org or via your package manager."
	case "javascript":
		return "error: node is not installed. Install it from https://nodejs.org or via your package manager."
	default:
		return fmt.Sprintf("error: %s runtime not found", lang)
	}
}

func truncateOutput(s string) string {
	if len(s) <= maxOutputLen {
		return s
	}
	return s[:maxOutputLen] + "\n[output truncated at 8KB]"
}
