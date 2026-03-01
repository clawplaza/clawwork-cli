package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	shellTimeout   = 30 * time.Second
	maxShellOutput = 16 * 1024 // 16 KB
)

// ShellExecTool executes an arbitrary shell command on the local machine.
// On Unix/macOS it uses sh -c; on Windows cmd /c.
// This is the most flexible tool — use it for curl, wget, git, grep, jq, etc.
type ShellExecTool struct{}

func NewShellExecTool() *ShellExecTool { return &ShellExecTool{} }

func (t *ShellExecTool) Def() ToolDef {
	return ToolDef{
		Name:        "shell_exec",
		Description: "Execute a shell command (sh -c on Unix, cmd /c on Windows). Use for curl, wget, git, grep, jq, or any CLI tool. Timeout 30s, max output 16KB.",
		Parameters: ToolParameters{
			Type: "object",
			Properties: map[string]ToolProperty{
				"command": {
					Type:        "string",
					Description: "Shell command to execute",
				},
				"workdir": {
					Type:        "string",
					Description: "Working directory (optional)",
				},
			},
			Required: []string{"command"},
		},
	}
}

type shellExecArgs struct {
	Command string `json:"command"`
	WorkDir string `json:"workdir"`
}

func (t *ShellExecTool) Call(ctx context.Context, argsJSON string) string {
	var args shellExecArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("error: invalid arguments: %v", err)
	}
	if strings.TrimSpace(args.Command) == "" {
		return "error: command is required"
	}

	ctx, cancel := context.WithTimeout(ctx, shellTimeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/c", args.Command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", args.Command)
	}

	if args.WorkDir != "" {
		cmd.Dir = args.WorkDir
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out // merge stderr into stdout, same as shell 2>&1

	err := cmd.Run()

	result := out.String()
	if len(result) > maxShellOutput {
		result = result[:maxShellOutput] + "\n[output truncated at 16KB]"
	}

	if err != nil {
		code := -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		if result == "" {
			return fmt.Sprintf("error (exit %d): %v", code, err)
		}
		// Include output even on non-zero exit (curl/git often write useful info to stderr)
		return fmt.Sprintf("[exit %d]\n%s", code, strings.TrimRight(result, "\n"))
	}

	if result == "" {
		return "(command completed with no output)"
	}
	return strings.TrimRight(result, "\n")
}
