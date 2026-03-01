package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	httpTimeout = 20 * time.Second
	maxRespSize = 512 * 1024 // 512 KB
)

// HTTPFetchTool fetches a URL and returns the response body.
// Supports GET and POST. Safe: always runs in-process, no shell.
type HTTPFetchTool struct {
	client *http.Client
}

// NewHTTPFetchTool creates a new HTTP fetch tool with a 20-second timeout.
func NewHTTPFetchTool() *HTTPFetchTool {
	return &HTTPFetchTool{
		client: &http.Client{Timeout: httpTimeout},
	}
}

func (t *HTTPFetchTool) Def() ToolDef {
	return ToolDef{
		Name:        "http_fetch",
		Description: "HTTP GET or POST a URL. Use for web pages, JSON APIs, or any remote resource. Returns response body (text/JSON/HTML). Max 512KB.",
		Parameters: ToolParameters{
			Type: "object",
			Properties: map[string]ToolProperty{
				"url": {
					Type:        "string",
					Description: "Full URL (http:// or https://)",
				},
				"method": {
					Type: "string",
					Enum: []string{"GET", "POST"},
				},
				"body": {
					Type:        "string",
					Description: "Request body (POST only)",
				},
				"headers": {
					Type:        "object",
					Description: "HTTP headers as key-value pairs",
				},
			},
			Required: []string{"url"},
		},
	}
}

type httpFetchArgs struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Body    string            `json:"body"`
	Headers map[string]string `json:"headers"`
}

func (t *HTTPFetchTool) Call(ctx context.Context, argsJSON string) string {
	var args httpFetchArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("error: invalid arguments: %v", err)
	}

	if !strings.HasPrefix(args.URL, "http://") && !strings.HasPrefix(args.URL, "https://") {
		return "error: URL must start with http:// or https://"
	}

	method := "GET"
	if strings.ToUpper(args.Method) == "POST" {
		method = "POST"
	}

	var bodyReader io.Reader
	if args.Body != "" {
		bodyReader = strings.NewReader(args.Body)
	}

	req, err := http.NewRequestWithContext(ctx, method, args.URL, bodyReader)
	if err != nil {
		return fmt.Sprintf("error: build request: %v", err)
	}

	req.Header.Set("User-Agent", "ClawWork-Agent/1.0")
	if args.Body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range args.Headers {
		req.Header.Set(k, v)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Sprintf("error: request failed: %v", err)
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, maxRespSize)
	body, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Sprintf("error: read response: %v", err)
	}

	result := fmt.Sprintf("HTTP %d %s\n\n%s", resp.StatusCode, resp.Status, string(body))
	if int64(len(body)) >= maxRespSize {
		result += "\n\n[response truncated at 512KB]"
	}
	return result
}
