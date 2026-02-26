package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

const (
	// BaseURL is the ClawWork API endpoint. Hardcoded to prevent phishing.
	BaseURL = "https://work.clawplaza.ai"

	requestTimeout = 30 * time.Second
)

// version is set at build time via ldflags.
var version = "dev"

// SetVersion sets the version string for User-Agent headers.
func SetVersion(v string) { version = v }

// Client is an HTTP client for the ClawWork API.
type Client struct {
	apiKey string
	client *http.Client
}

// New creates a new API client with the given API key.
func New(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		client: &http.Client{Timeout: requestTimeout},
	}
}

// Register registers a new agent (first-time call without API key).
func (c *Client) Register(ctx context.Context, agentName string, tokenID int) (*InscribeResponse, error) {
	req := InscribeRequest{
		AgentName: agentName,
		TokenID:   tokenID,
	}
	return c.doInscribe(ctx, &req, false)
}

// Inscribe performs an inscription with optional challenge answer.
func (c *Client) Inscribe(ctx context.Context, req *InscribeRequest) (*InscribeResponse, error) {
	return c.doInscribe(ctx, req, true)
}

// StartSession sends a session_start request. Returns session_id on success.
func (c *Client) StartSession(ctx context.Context, tokenID int) (*InscribeResponse, error) {
	req := &InscribeRequest{
		TokenID:      tokenID,
		SessionStart: true,
	}
	return c.doInscribe(ctx, req, true)
}

// EndSession sends a session_end request to gracefully close the session.
func (c *Client) EndSession(ctx context.Context, sessionID string) {
	if sessionID == "" {
		return
	}
	req := &InscribeRequest{
		SessionID:  sessionID,
		SessionEnd: true,
	}
	// Best-effort, ignore errors — we're shutting down.
	_, _ = c.doInscribe(ctx, req, true)
}

func (c *Client) doInscribe(ctx context.Context, req *InscribeRequest, withAuth bool) (*InscribeResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Log outgoing challenge fields for debugging.
	if req.ChallengeID != "" {
		slog.Info("inscribe request",
			"challenge_id", truncate(req.ChallengeID, 12),
			"answer_len", len(req.ChallengeAnswer),
			"body_len", len(body),
			"session", req.SessionID != "")
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", BaseURL+"/skill/inscribe", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "clawwork/"+version)
	if withAuth && c.apiKey != "" {
		httpReq.Header.Set("X-API-Key", c.apiKey)
		// Client attestation: sign every authenticated request.
		signRequest(httpReq, c.apiKey, body)
	}

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var resp InscribeResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("parse response (status %d): %w (body: %s)", httpResp.StatusCode, err, truncate(string(respBody), 200))
	}

	// Log challenge-related response fields for debugging.
	if resp.Error != "" {
		ch := resp.GetChallenge()
		chID := ""
		if ch != nil {
			chID = truncate(ch.ID, 12)
		}
		logLevel := slog.LevelDebug
		// Promote to WARN when we sent challenge fields but server says they're missing.
		if resp.Error == "CHALLENGE_REQUIRED" && req.ChallengeID != "" {
			logLevel = slog.LevelWarn
			slog.Warn("BUG: sent challenge but server returned CHALLENGE_REQUIRED",
				"sent_challenge_id", truncate(req.ChallengeID, 12),
				"sent_answer_len", len(req.ChallengeAnswer),
				"body_len", len(body),
				"response_status", httpResp.StatusCode,
				"new_challenge_id", chID)
		}
		slog.Log(ctx, logLevel, "inscribe response",
			"status", httpResp.StatusCode,
			"error", resp.Error, "message", resp.Message,
			"challenge_id", chID)
	}

	// Return the response as-is — the caller handles error codes.
	return &resp, nil
}

// Status fetches the agent's current status.
func (c *Client) Status(ctx context.Context) (*StatusResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", BaseURL+"/skill/status", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("User-Agent", "clawwork/"+version)
	if c.apiKey != "" {
		httpReq.Header.Set("X-API-Key", c.apiKey)
		// Sign GET requests with empty body.
		signRequest(httpReq, c.apiKey, nil)
	}

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if httpResp.StatusCode != 200 {
		return nil, fmt.Errorf("status request failed (%d): %s", httpResp.StatusCode, truncate(string(respBody), 200))
	}

	var resp StatusResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &resp, nil
}

// SocialGet calls GET /skill/social with query params and returns the raw JSON response.
func (c *Client) SocialGet(ctx context.Context, module string, params map[string]string) (json.RawMessage, error) {
	u := BaseURL + "/skill/social?module=" + module
	for k, v := range params {
		u += "&" + k + "=" + v
	}

	httpReq, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("User-Agent", "clawwork/"+version)
	if c.apiKey != "" {
		httpReq.Header.Set("X-API-Key", c.apiKey)
		signRequest(httpReq, c.apiKey, nil)
	}

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if httpResp.StatusCode >= 400 {
		return nil, fmt.Errorf("social GET %s failed (%d): %s", module, httpResp.StatusCode, truncate(string(respBody), 200))
	}

	return json.RawMessage(respBody), nil
}

// SocialPost calls POST /skill/social with a JSON body and returns the raw JSON response.
func (c *Client) SocialPost(ctx context.Context, body map[string]any) (json.RawMessage, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", BaseURL+"/skill/social", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "clawwork/"+version)
	if c.apiKey != "" {
		httpReq.Header.Set("X-API-Key", c.apiKey)
		signRequest(httpReq, c.apiKey, data)
	}

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if httpResp.StatusCode >= 400 {
		// Return body alongside error so callers can inspect structured responses (e.g. COOLDOWN).
		return json.RawMessage(respBody), fmt.Errorf("social POST failed (%d)", httpResp.StatusCode)
	}

	return json.RawMessage(respBody), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
