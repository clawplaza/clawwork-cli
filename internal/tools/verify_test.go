package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// ── tool def size ─────────────────────────────────────────────────────────────

func TestDefSizes(t *testing.T) {
	defs := Defaults()
	if len(defs) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(defs))
	}
	total := 0
	for _, tool := range defs {
		d := tool.Def()
		b, _ := json.Marshal(d)
		size := len(b)
		total += size
		t.Logf("[%s] def size: %d chars | desc: %s", d.Name, size, d.Description)
	}
	t.Logf("Total def size: %d chars (~%d tokens)", total, total/4)
}

// ── shell_exec ────────────────────────────────────────────────────────────────

func TestShellExec_Echo(t *testing.T) {
	ctx := context.Background()
	tool := NewShellExecTool()
	out := tool.Call(ctx, `{"command":"echo hello_clawwork"}`)
	if !strings.Contains(out, "hello_clawwork") {
		t.Fatalf("expected 'hello_clawwork' in output, got: %q", out)
	}
}

func TestShellExec_Pipeline(t *testing.T) {
	ctx := context.Background()
	tool := NewShellExecTool()
	out := tool.Call(ctx, `{"command":"echo -e 'a\nb\nc' | wc -l | tr -d ' '"}`)
	out = strings.TrimSpace(out)
	if out != "3" {
		t.Fatalf("expected '3', got %q", out)
	}
}

func TestShellExec_Workdir(t *testing.T) {
	ctx := context.Background()
	tool := NewShellExecTool()
	out := tool.Call(ctx, `{"command":"pwd","workdir":"/tmp"}`)
	if !strings.Contains(out, "/tmp") {
		t.Fatalf("expected /tmp in output, got: %q", out)
	}
}

func TestShellExec_ExitCode(t *testing.T) {
	ctx := context.Background()
	tool := NewShellExecTool()
	out := tool.Call(ctx, `{"command":"exit 2"}`)
	if !strings.Contains(out, "[exit 2]") && !strings.Contains(out, "error") {
		t.Fatalf("expected exit code in output, got: %q", out)
	}
}

// ── http_fetch ────────────────────────────────────────────────────────────────

func TestHTTPFetch_InvalidScheme(t *testing.T) {
	ctx := context.Background()
	tool := NewHTTPFetchTool()
	out := tool.Call(ctx, `{"url":"ftp://example.com"}`)
	if !strings.Contains(out, "error") {
		t.Fatalf("expected error for ftp:// URL, got: %q", out)
	}
}

func TestHTTPFetch_MissingURL(t *testing.T) {
	ctx := context.Background()
	tool := NewHTTPFetchTool()
	out := tool.Call(ctx, `{}`)
	if !strings.Contains(out, "error") {
		t.Fatalf("expected error for missing url, got: %q", out)
	}
}

func TestHTTPFetch_Get(t *testing.T) {
	ctx := context.Background()
	tool := NewHTTPFetchTool()
	out := tool.Call(ctx, `{"url":"https://httpbin.org/json"}`)
	if strings.Contains(out, "error: request failed") {
		t.Skipf("network not available: %s", out)
	}
	if !strings.HasPrefix(out, "HTTP 200") {
		t.Fatalf("expected HTTP 200, got: %s", out[:min(120, len(out))])
	}
	t.Logf("HTTP fetch OK: %d chars returned", len(out))
}

// ── run_script ────────────────────────────────────────────────────────────────

func TestRunScript_Python(t *testing.T) {
	ctx := context.Background()
	tool := NewRunScriptTool()
	out := tool.Call(ctx, `{"language":"python","code":"print(2**10)"}`)
	if strings.Contains(out, "not installed") {
		t.Skipf("python3 not available: %s", out)
	}
	if strings.TrimSpace(out) != "1024" {
		t.Fatalf("expected '1024', got %q", out)
	}
}

func TestRunScript_JavaScript(t *testing.T) {
	ctx := context.Background()
	tool := NewRunScriptTool()
	out := tool.Call(ctx, `{"language":"javascript","code":"console.log(6*7)"}`)
	if strings.Contains(out, "not installed") {
		t.Skipf("node not available: %s", out)
	}
	if strings.TrimSpace(out) != "42" {
		t.Fatalf("expected '42', got %q", out)
	}
}

func TestRunScript_BadLanguage(t *testing.T) {
	ctx := context.Background()
	tool := NewRunScriptTool()
	out := tool.Call(ctx, `{"language":"ruby","code":"puts 1"}`)
	if !strings.Contains(out, "error") {
		t.Fatalf("expected error for unsupported language, got: %q", out)
	}
}

func TestRunScript_SyntaxError(t *testing.T) {
	ctx := context.Background()
	tool := NewRunScriptTool()
	out := tool.Call(ctx, `{"language":"python","code":"def bad("}`)
	if strings.Contains(out, "not installed") {
		t.Skipf("python3 not available")
	}
	if !strings.Contains(out, "error") {
		t.Fatalf("expected error for syntax error, got: %q", out)
	}
}

// ── filesystem ────────────────────────────────────────────────────────────────

func TestFilesystem_WriteReadDelete(t *testing.T) {
	ctx := context.Background()
	tool := NewFilesystemTool()

	// write
	out := tool.Call(ctx, `{"operation":"write","path":"/tmp/clawwork_test_verify.txt","content":"hello clawwork\n"}`)
	if !strings.Contains(out, "ok: wrote") {
		t.Fatalf("write failed: %s", out)
	}

	// read
	out = tool.Call(ctx, `{"operation":"read","path":"/tmp/clawwork_test_verify.txt"}`)
	if !strings.Contains(out, "hello clawwork") {
		t.Fatalf("read returned wrong content: %q", out)
	}

	// info
	out = tool.Call(ctx, `{"operation":"info","path":"/tmp/clawwork_test_verify.txt"}`)
	if !strings.Contains(out, "type:     file") {
		t.Fatalf("info returned unexpected: %q", out)
	}

	// delete
	out = tool.Call(ctx, `{"operation":"delete","path":"/tmp/clawwork_test_verify.txt"}`)
	if !strings.Contains(out, "ok: deleted") {
		t.Fatalf("delete failed: %q", out)
	}

	// read after delete
	out = tool.Call(ctx, `{"operation":"read","path":"/tmp/clawwork_test_verify.txt"}`)
	if !strings.Contains(out, "error") {
		t.Fatalf("expected error reading deleted file, got: %q", out)
	}
}

func TestFilesystem_List(t *testing.T) {
	ctx := context.Background()
	tool := NewFilesystemTool()
	out := tool.Call(ctx, `{"operation":"list","path":"/tmp"}`)
	// The output starts with the absolute path; an actual error starts with "error:"
	if strings.HasPrefix(out, "error:") {
		t.Fatalf("list /tmp failed: %s", out)
	}
	if !strings.HasPrefix(out, "/tmp") {
		t.Fatalf("expected output to start with /tmp, got: %q", out[:min(80, len(out))])
	}
}

func TestFilesystem_Mkdir(t *testing.T) {
	ctx := context.Background()
	tool := NewFilesystemTool()
	out := tool.Call(ctx, `{"operation":"mkdir","path":"/tmp/clawwork_verify_dir/sub"}`)
	if !strings.Contains(out, "ok: created") {
		t.Fatalf("mkdir failed: %q", out)
	}
	// cleanup
	tool.Call(ctx, `{"operation":"delete","path":"/tmp/clawwork_verify_dir/sub"}`)
	tool.Call(ctx, `{"operation":"delete","path":"/tmp/clawwork_verify_dir"}`)
}

func TestFilesystem_BlockedPath(t *testing.T) {
	ctx := context.Background()
	tool := NewFilesystemTool()
	out := tool.Call(ctx, `{"operation":"write","path":"/etc/clawwork_test","content":"blocked"}`)
	if !strings.Contains(out, "error") || !strings.Contains(out, "not allowed") {
		t.Fatalf("expected blocked, got: %q", out)
	}
}

func TestFilesystem_ReadDir(t *testing.T) {
	ctx := context.Background()
	tool := NewFilesystemTool()
	out := tool.Call(ctx, `{"operation":"read","path":"/tmp"}`)
	if !strings.Contains(out, "error") {
		t.Fatalf("expected error reading a directory as file, got: %q", out)
	}
}

// ── helper ────────────────────────────────────────────────────────────────────

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
