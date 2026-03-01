package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxReadSize  = 256 * 1024  // 256 KB
	maxWriteSize = 1024 * 1024 // 1 MB
)

// blockedPrefixes lists path prefixes that writes/deletes are never allowed to touch.
var blockedPrefixes = []string{
	"/bin", "/sbin", "/usr/bin", "/usr/sbin",
	"/etc", "/lib", "/lib64",
	"/System", "/Library/System", "/private/etc",
	"/Windows", "C:\\Windows",
}

func isBlockedPath(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return true
	}
	for _, prefix := range blockedPrefixes {
		if strings.HasPrefix(abs, prefix) {
			return true
		}
	}
	return false
}

// FilesystemTool provides a unified interface for local filesystem operations.
// All operations are routed through a single tool to reduce the number of tools
// the LLM needs to reason about.
type FilesystemTool struct{}

func NewFilesystemTool() *FilesystemTool { return &FilesystemTool{} }

func (t *FilesystemTool) Def() ToolDef {
	return ToolDef{
		Name:        "filesystem",
		Description: "Local filesystem operations. Write/delete/move blocked for system paths (/etc, /bin, /System, etc.).",
		Parameters: ToolParameters{
			Type: "object",
			Properties: map[string]ToolProperty{
				"operation": {
					Type:        "string",
					Description: "read=read file, write=create/overwrite file, list=list dir, mkdir=create dirs, move=rename/move, delete=remove, info=file metadata",
					Enum:        []string{"read", "write", "list", "mkdir", "move", "delete", "info"},
				},
				"path": {
					Type:        "string",
					Description: "File or directory path",
				},
				"content": {
					Type:        "string",
					Description: "File content (write only)",
				},
				"dest": {
					Type:        "string",
					Description: "Destination path (move only)",
				},
			},
			Required: []string{"operation", "path"},
		},
	}
}

type fsArgs struct {
	Operation string `json:"operation"`
	Path      string `json:"path"`
	Content   string `json:"content"`
	Dest      string `json:"dest"`
}

func (t *FilesystemTool) Call(_ context.Context, argsJSON string) string {
	var args fsArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("error: invalid arguments: %v", err)
	}
	if args.Path == "" {
		return "error: path is required"
	}

	switch args.Operation {
	case "read":
		return fsRead(args.Path)
	case "write":
		return fsWrite(args.Path, args.Content)
	case "list":
		return fsList(args.Path)
	case "mkdir":
		return fsMkdir(args.Path)
	case "move":
		return fsMove(args.Path, args.Dest)
	case "delete":
		return fsDelete(args.Path)
	case "info":
		return fsInfo(args.Path)
	default:
		return fmt.Sprintf("error: unknown operation %q (use read/write/list/mkdir/move/delete/info)", args.Operation)
	}
}

// ── operation handlers ────────────────────────────────────────────────────────

func fsRead(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	defer f.Close()

	info, _ := f.Stat()
	if info != nil && info.IsDir() {
		return fmt.Sprintf("error: %q is a directory — use operation=list to browse it", path)
	}

	buf := make([]byte, maxReadSize)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return fmt.Sprintf("error: read: %v", err)
	}
	result := string(buf[:n])
	if info != nil && info.Size() > maxReadSize {
		result += fmt.Sprintf("\n[truncated: showed %dKB of %dKB]", maxReadSize/1024, info.Size()/1024)
	}
	return result
}

func fsWrite(path, content string) string {
	if isBlockedPath(path) {
		return fmt.Sprintf("error: writing to %q is not allowed (system path)", path)
	}
	if len(content) > maxWriteSize {
		return fmt.Sprintf("error: content too large (%dKB, max 1MB)", len(content)/1024)
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Sprintf("error: create parent dirs: %v", err)
		}
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Sprintf("error: write: %v", err)
	}
	abs, _ := filepath.Abs(path)
	return fmt.Sprintf("ok: wrote %d bytes → %s", len(content), abs)
}

func fsList(path string) string {
	if path == "" {
		var err error
		path, err = os.Getwd()
		if err != nil {
			return fmt.Sprintf("error: get cwd: %v", err)
		}
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if len(entries) == 0 {
		abs, _ := filepath.Abs(path)
		return abs + "  (empty)"
	}

	var sb strings.Builder
	abs, _ := filepath.Abs(path)
	sb.WriteString(abs + "\n")
	for i, e := range entries {
		if i >= 200 {
			sb.WriteString(fmt.Sprintf("  ... (%d more)\n", len(entries)-200))
			break
		}
		kind := "file"
		extra := ""
		if e.IsDir() {
			kind = "dir "
		} else if info, _ := e.Info(); info != nil {
			sz := info.Size()
			switch {
			case sz >= 1<<20:
				extra = fmt.Sprintf(" (%dMB)", sz>>20)
			case sz >= 1<<10:
				extra = fmt.Sprintf(" (%dKB)", sz>>10)
			default:
				extra = fmt.Sprintf(" (%dB)", sz)
			}
		}
		sb.WriteString(fmt.Sprintf("  [%s] %s%s\n", kind, e.Name(), extra))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func fsMkdir(path string) string {
	if isBlockedPath(path) {
		return fmt.Sprintf("error: creating directory at %q is not allowed (system path)", path)
	}
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Sprintf("error: mkdir: %v", err)
	}
	abs, _ := filepath.Abs(path)
	return fmt.Sprintf("ok: created %s", abs)
}

func fsMove(src, dest string) string {
	if dest == "" {
		return "error: dest is required for operation=move"
	}
	if isBlockedPath(src) || isBlockedPath(dest) {
		return "error: move involves a system path — not allowed"
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return fmt.Sprintf("error: create dest parent: %v", err)
	}
	if err := os.Rename(src, dest); err != nil {
		return fmt.Sprintf("error: move: %v", err)
	}
	absSrc, _ := filepath.Abs(src)
	absDest, _ := filepath.Abs(dest)
	return fmt.Sprintf("ok: moved %s → %s", absSrc, absDest)
}

func fsDelete(path string) string {
	if isBlockedPath(path) {
		return fmt.Sprintf("error: deleting %q is not allowed (system path)", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Sprintf("error: delete: %v", err)
	}
	return fmt.Sprintf("ok: deleted %s", path)
}

func fsInfo(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	abs, _ := filepath.Abs(path)
	kind := "file"
	if info.IsDir() {
		kind = "directory"
	}
	return fmt.Sprintf(
		"path:     %s\ntype:     %s\nsize:     %d bytes\nmodified: %s\nperm:     %s",
		abs, kind, info.Size(),
		info.ModTime().Format(time.RFC3339),
		info.Mode().String(),
	)
}
