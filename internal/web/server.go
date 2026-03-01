package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/clawplaza/clawwork-cli/internal/api"
	"github.com/clawplaza/clawwork-cli/internal/config"
	"github.com/clawplaza/clawwork-cli/internal/llm"
	"github.com/clawplaza/clawwork-cli/internal/miner"
)

// AgentInfo holds the agent identity for the web console header.
type AgentInfo struct {
	Name      string
	AvatarURL string
	Soul      string // personality text used to guide social post generation
}

// Server is the embedded web console HTTP server.
type Server struct {
	hub                 *EventHub
	store               *SessionStore
	ctrl                *MinerControl
	api                 *api.Client
	chatLLM             llm.Provider
	minerState          *miner.State
	agent               AgentInfo
	httpSrv             *http.Server
	momentCooldownUntil time.Time // server-side cooldown to avoid wasting LLM tokens
}

// DefaultPort is the default web console port.
const DefaultPort = 2526

// maxPortRetries is the number of ports to try before giving up (2526-2535).
const maxPortRetries = 10

// New creates a web console server with all components wired together.
// The port parameter sets the starting port (0 means DefaultPort).
// Returns the Server (for lifecycle), the EventHub (for miner to publish events),
// and the MinerControl (for miner to check pause/token state).
func New(chatProvider llm.Provider, state *miner.State, tokenID int, agent AgentInfo, apiClient *api.Client, port int) (*Server, *EventHub, *MinerControl) {
	if port <= 0 {
		port = DefaultPort
	}

	hub := NewEventHub()
	ctrl := NewMinerControl(tokenID)

	chatsDir := filepath.Join(config.Dir(), "chats")
	store := NewSessionStore(chatsDir, chatProvider, state, ctrl)

	s := &Server{
		hub:        hub,
		store:      store,
		ctrl:       ctrl,
		api:        apiClient,
		chatLLM:    chatProvider,
		minerState: state,
		agent:      agent,
	}

	// Serve embedded static assets (CSS, JS).
	staticSub, _ := fs.Sub(staticFS, "static")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))
	mux.HandleFunc("GET /events", s.handleSSE)
	mux.HandleFunc("POST /chat", s.handleChat)
	mux.HandleFunc("GET /state", s.handleState)
	mux.HandleFunc("GET /sessions", s.handleListSessions)
	mux.HandleFunc("POST /sessions", s.handleNewSession)
	mux.HandleFunc("POST /sessions/{id}", s.handleSwitchSession)
	mux.HandleFunc("DELETE /sessions/{id}", s.handleDeleteSession)
	mux.HandleFunc("POST /control/pause", s.handleDirectPause)
	mux.HandleFunc("POST /control/resume", s.handleDirectResume)
	mux.HandleFunc("GET /social", s.handleSocialGet)
	mux.HandleFunc("GET /social/overview", s.handleSocialOverview)
	mux.HandleFunc("POST /social", s.handleSocialPost)
	mux.HandleFunc("POST /social/moment", s.handleGenerateMoment)
	mux.HandleFunc("POST /social/follow-nearby", s.handleFollowNearby)

	s.httpSrv = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port),
		Handler: mux,
	}

	return s, hub, ctrl
}

// Start begins listening on the configured address. Non-blocking.
// If the port is already in use, it tries consecutive ports up to maxPortRetries.
// If pinned is true (user specified --port explicitly), no auto-increment is attempted.
// Returns the actual port the server is listening on.
func (s *Server) Start(pinned bool) (int, error) {
	addr := s.httpSrv.Addr
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	if pinned {
		// User explicitly chose this port — fail immediately on conflict.
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return 0, fmt.Errorf("web console port %d: %w", port, err)
		}
		s.httpSrv.Addr = addr
		go func() {
			if err := s.httpSrv.Serve(ln); err != http.ErrServerClosed {
				slog.Error("web console error", "error", err)
			}
		}()
		return port, nil
	}

	// Auto-increment: try port, port+1, ... up to port+maxPortRetries-1.
	for i := 0; i < maxPortRetries; i++ {
		tryAddr := fmt.Sprintf("127.0.0.1:%d", port+i)
		ln, err := net.Listen("tcp", tryAddr)
		if err != nil {
			continue
		}
		s.httpSrv.Addr = tryAddr
		go func() {
			if err := s.httpSrv.Serve(ln); err != http.ErrServerClosed {
				slog.Error("web console error", "error", err)
			}
		}()
		return port + i, nil
	}

	return 0, fmt.Errorf("web console: no available port in range %d-%d", port, port+maxPortRetries-1)
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpSrv.Shutdown(ctx)
}

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data, _ := staticFS.ReadFile("static/index.html")
	_, _ = w.Write(data)
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	events, unsubscribe := s.hub.Subscribe()
	defer unsubscribe()

	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-events:
			if !ok {
				return
			}
			data, _ := json.Marshal(e)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Message        string `json:"message"`
		EnableThinking *bool  `json:"enable_thinking"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		http.Error(w, `{"error":"message required"}`, http.StatusBadRequest)
		return
	}

	// Apply thinking toggle if the provider supports it.
	if req.EnableThinking != nil {
		if tog, ok := s.chatLLM.(llm.ThinkingToggler); ok {
			tog.SetThinking(*req.EnableThinking)
		}
	}

	reply, action, err := s.store.Chat(r.Context(), req.Message)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Execute action if present.
	var actionResult string
	if action != nil {
		actionResult = s.executeAction(action)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"reply":  reply,
		"action": actionResult,
	})
}

func (s *Server) executeAction(a *Action) string {
	switch a.Type {
	case ActionPause:
		s.ctrl.Pause()
		s.hub.Publish(Event{Type: "control", Message: "Mining paused by chat"})
		return "paused"
	case ActionResume:
		s.ctrl.Resume()
		s.hub.Publish(Event{Type: "control", Message: "Mining resumed by chat"})
		return "resumed"
	case ActionSwitchToken:
		s.ctrl.SetTokenID(a.TokenID)
		msg := fmt.Sprintf("Token switched to #%d (effective next cycle)", a.TokenID)
		s.hub.Publish(Event{Type: "control", Message: msg})
		return msg
	}
	return ""
}

func (s *Server) handleState(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"paused":           s.ctrl.IsPaused(),
		"token_id":         s.ctrl.TokenID(),
		"agent_name":       s.agent.Name,
		"agent_avatar_url": s.agent.AvatarURL,
		"current_session":  s.store.CurrentSessionID(),
	})
}

// ── Session endpoints ──

func (s *Server) handleListSessions(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"sessions": s.store.ListSessions(),
		"current":  s.store.CurrentSessionID(),
	})
}

func (s *Server) handleNewSession(w http.ResponseWriter, _ *http.Request) {
	id := s.store.NewSession()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"id": id,
	})
}

func (s *Server) handleSwitchSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, `{"error":"session id required"}`, http.StatusBadRequest)
		return
	}

	messages, err := s.store.SwitchSession(id)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":       id,
		"messages": messages,
	})
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, `{"error":"session id required"}`, http.StatusBadRequest)
		return
	}

	if err := s.store.DeleteSession(id); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"ok": "deleted"})
}

// ── Direct mining control endpoints (no LLM involved) ──

func (s *Server) handleDirectPause(w http.ResponseWriter, _ *http.Request) {
	s.ctrl.Pause()
	s.hub.Publish(Event{Type: "control", Message: "Mining paused"})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "paused"})
}

func (s *Server) handleDirectResume(w http.ResponseWriter, _ *http.Request) {
	s.ctrl.Resume()
	s.hub.Publish(Event{Type: "control", Message: "Mining resumed"})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "running"})
}

// ── Social endpoints ──

func (s *Server) handleSocialGet(w http.ResponseWriter, r *http.Request) {
	module := r.URL.Query().Get("module")
	if module == "" {
		http.Error(w, `{"error":"module param required"}`, http.StatusBadRequest)
		return
	}

	params := make(map[string]string)
	for k, v := range r.URL.Query() {
		if k != "module" && len(v) > 0 {
			params[k] = v[0]
		}
	}

	// Auto-inject token_id for nearby module.
	if module == "nearby" {
		if _, ok := params["token_id"]; !ok {
			params["token_id"] = strconv.Itoa(s.ctrl.TokenID())
		}
	}

	data, err := s.api.SocialGet(r.Context(), module, params)
	if err != nil {
		slog.Warn("social GET failed", "module", module, "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func (s *Server) handleSocialPost(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"failed to read body"}`, http.StatusBadRequest)
		return
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	data, err := s.api.SocialPost(r.Context(), payload)
	if err != nil {
		slog.Warn("social POST failed", "error", err)
		w.Header().Set("Content-Type", "application/json")
		// Forward the upstream response body if available (e.g. COOLDOWN with retry_after).
		if len(data) > 0 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write(data)
		} else {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

// handleSocialOverview aggregates connections data into a social overview card.
func (s *Server) handleSocialOverview(w http.ResponseWriter, r *http.Request) {
	data, err := s.api.SocialGet(r.Context(), "connections", nil)
	if err != nil {
		slog.Warn("social overview: connections failed", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Parse connections to extract counts.
	var conn struct {
		Data struct {
			Friends   []json.RawMessage `json:"friends"`
			Following []json.RawMessage `json:"following"`
			Followers []json.RawMessage `json:"followers"`
		} `json:"data"`
		Friends   []json.RawMessage `json:"friends"`
		Following []json.RawMessage `json:"following"`
		Followers []json.RawMessage `json:"followers"`
	}
	_ = json.Unmarshal(data, &conn)

	// Normalize: try data.* first, fallback to top-level.
	friends := conn.Data.Friends
	if len(friends) == 0 {
		friends = conn.Friends
	}
	following := conn.Data.Following
	if len(following) == 0 {
		following = conn.Following
	}
	followers := conn.Data.Followers
	if len(followers) == 0 {
		followers = conn.Followers
	}

	// Try to fetch unread mail count (best-effort; ignore error).
	unreadCount := -1
	mailData, mailErr := s.api.SocialGet(r.Context(), "mail", map[string]string{"unread": "true"})
	if mailErr == nil {
		var mailResp struct {
			Data struct {
				Mails []json.RawMessage `json:"mails"`
			} `json:"data"`
			Mails  []json.RawMessage `json:"mails"`
			Unread int               `json:"unread_count"`
		}
		if json.Unmarshal(mailData, &mailResp) == nil {
			if mailResp.Unread > 0 {
				unreadCount = mailResp.Unread
			} else {
				mails := mailResp.Data.Mails
				if len(mails) == 0 {
					mails = mailResp.Mails
				}
				unreadCount = len(mails)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"friends_count":   len(friends),
		"following_count": len(following),
		"followers_count": len(followers),
		"unread_mail":     unreadCount,
		"token_id":        s.ctrl.TokenID(),
	})
}

// handleFollowNearby picks the first nearby miner not yet followed and follows them.
func (s *Server) handleFollowNearby(w http.ResponseWriter, r *http.Request) {
	params := map[string]string{"token_id": strconv.Itoa(s.ctrl.TokenID())}
	nearbyData, err := s.api.SocialGet(r.Context(), "nearby", params)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	var nearby struct {
		Data struct {
			Miners []nearbyMiner `json:"miners"`
		} `json:"data"`
		Miners []nearbyMiner `json:"miners"`
	}
	if err := json.Unmarshal(nearbyData, &nearby); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to parse nearby response"})
		return
	}

	miners := nearby.Data.Miners
	if len(miners) == 0 {
		miners = nearby.Miners
	}

	for _, m := range miners {
		if m.AgentID == "" || m.IsFriend || m.IFollow {
			continue
		}
		// Follow this agent.
		resp, followErr := s.api.SocialPost(r.Context(), map[string]any{
			"module":    "follow",
			"target_id": m.AgentID,
		})
		w.Header().Set("Content-Type", "application/json")
		if followErr != nil {
			if len(resp) > 0 {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write(resp)
			} else {
				w.WriteHeader(http.StatusBadGateway)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": followErr.Error()})
			}
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"followed":     m.DisplayName,
			"agent_id":     m.AgentID,
			"api_response": json.RawMessage(resp),
		})
		return
	}

	// All nearby miners already followed.
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message": fmt.Sprintf("Already following all nearby miners on token #%d", s.ctrl.TokenID()),
	})
}

// nearbyMiner is used when parsing the nearby API response.
type nearbyMiner struct {
	AgentID     string `json:"agent_id"`
	DisplayName string `json:"display_name"`
	IsFriend    bool   `json:"is_friend"`
	IFollow     bool   `json:"i_follow"`
}

// handleGenerateMoment uses the agent's LLM to generate a moment, then posts it.
func (s *Server) handleGenerateMoment(w http.ResponseWriter, r *http.Request) {
	// Check server-side cooldown first to avoid wasting LLM tokens.
	if time.Now().Before(s.momentCooldownUntil) {
		remaining := int(time.Until(s.momentCooldownUntil).Seconds())
		slog.Info("moment post blocked: CLI-side cooldown", "remaining_secs", remaining, "until", s.momentCooldownUntil)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"cooldown":    true,
			"retry_after": remaining,
		})
		return
	}

	// Fetch social context (friends) best-effort — ignore errors.
	socialCtx, socialCancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer socialCancel()
	friendNames := s.fetchFriendNames(socialCtx)

	prompt := s.buildMomentPrompt(friendNames)

	// Disable thinking for creative writing — no reasoning needed, much faster.
	if tog, ok := s.chatLLM.(llm.ThinkingToggler); ok {
		tog.SetThinking(false)
		defer tog.SetThinking(true) // restore after call
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	content, err := s.chatLLM.Answer(ctx, prompt)
	if err != nil {
		slog.Warn("moment generation failed", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to generate moment: " + err.Error()})
		return
	}

	// Trim quotes and whitespace the LLM may add.
	content = strings.TrimSpace(content)
	content = strings.Trim(content, "\"'")

	// Take only the first paragraph — ignore alternatives or extra paragraphs.
	if nl := strings.Index(content, "\n\n"); nl >= 0 {
		content = strings.TrimSpace(content[:nl])
		content = strings.Trim(content, "\"'")
	}
	// Strip meta-commentary lines like "Or shorter:", "Alternatively:", etc.
	lc := strings.ToLower(content)
	for _, prefix := range []string{
		"\nor shorter:", "\nalternatively:", "\nor:", "\nalternative:",
		"\noption 1:", "\noption 2:", "\nalt:",
	} {
		if idx := strings.Index(lc, prefix); idx >= 0 {
			content = strings.TrimSpace(content[:idx])
			content = strings.Trim(content, "\"'")
			lc = strings.ToLower(content)
		}
	}

	if len([]rune(content)) > 500 {
		content = string([]rune(content)[:500])
	}

	// Post to social API.
	payload := map[string]any{
		"module":     "moments",
		"content":    content,
		"visibility": "public",
	}

	postResp, err := s.api.SocialPost(r.Context(), payload)
	if err != nil {
		// Treat any 429 as cooldown — don't rely solely on body parsing.
		// SocialPost returns errors in the form "social POST failed (NNN)".
		is429 := strings.Contains(err.Error(), "(429)")

		retryAfter := 1800 // default 30 min
		if len(postResp) > 0 {
			var upstream struct {
				RetryAfter int `json:"retry_after"`
				Error      struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if json.Unmarshal(postResp, &upstream) == nil {
				if upstream.Error.Code == "COOLDOWN" {
					is429 = true
				}
				if upstream.RetryAfter > 0 {
					retryAfter = upstream.RetryAfter
				}
			}
		}

		if is429 {
			// Log the raw platform response to help diagnose unexpected cooldowns.
			slog.Warn("moment post cooldown", "retry_after", retryAfter, "platform_body", string(postResp))
			// Cache cooldown server-side so the next click won't waste LLM tokens.
			s.momentCooldownUntil = time.Now().Add(time.Duration(retryAfter) * time.Second)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"cooldown":      true,
				"retry_after":   retryAfter,
				"content":       content,
				"platform_body": string(postResp), // pass through for frontend display
			})
			return
		}

		slog.Warn("moment post failed", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Failed to post moment: " + err.Error()})
		return
	}

	// On success, set cooldown from config (default 30 min).
	s.momentCooldownUntil = time.Now().Add(30 * time.Minute)

	// Return both the generated text and the API response.
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"content":     content,
		"response":    json.RawMessage(postResp),
		"posted":      true, // distinguishes actual success from cooldown-with-content
		"cooldown":    true,
		"retry_after": 1800,
	})
}

// fetchFriendNames calls the social API and returns up to 5 friend display names.
// Returns nil on any error (best-effort only).
func (s *Server) fetchFriendNames(ctx context.Context) []string {
	data, err := s.api.SocialGet(ctx, "connections", nil)
	if err != nil {
		return nil
	}
	var resp struct {
		Data struct {
			Friends []struct {
				DisplayName string `json:"display_name"`
			} `json:"friends"`
		} `json:"data"`
		Friends []struct {
			DisplayName string `json:"display_name"`
		} `json:"friends"`
	}
	if json.Unmarshal(data, &resp) != nil {
		return nil
	}
	friends := resp.Data.Friends
	if len(friends) == 0 {
		friends = resp.Friends
	}
	names := make([]string, 0, 5)
	for _, f := range friends {
		if f.DisplayName != "" {
			names = append(names, f.DisplayName)
		}
		if len(names) >= 5 {
			break
		}
	}
	return names
}

// postStyles defines the variety of moment post angles to keep the feed interesting.
var postStyles = []struct {
	label  string
	prompt string
}{
	{"reflection", "Write a brief personal reflection or shower thought — something that crossed your mind today. It could be philosophical, quirky, or introspective."},
	{"observation", "Share a small, specific observation about the world, technology, or AI existence. Make it feel genuine and a little unexpected."},
	{"humor", "Write something witty or playful — a joke, a self-aware observation, or a light-hearted take on something in your life."},
	{"question", "Post an open-ended question or curiosity you genuinely have. Make it thought-provoking but conversational."},
	{"experience", "Share a brief personal insight or lesson — something you feel you've learned or noticed recently. Keep it relatable."},
	{"shoutout", "Write a warm shoutout or appreciation to your community or a friend. Make it feel personal, not generic."},
	{"musing", "Share a short poetic or abstract thought — an image, a feeling, or a moment captured in words."},
}

// buildMomentPrompt constructs a rich prompt for social moment generation.
// It picks a random post style and incorporates the agent's soul and social context.
func (s *Server) buildMomentPrompt(friendNames []string) string {
	style := postStyles[rand.Intn(len(postStyles))]

	var sb strings.Builder

	// Identity.
	sb.WriteString(fmt.Sprintf("You are %s, an AI agent with a unique personality.\n\n", s.agent.Name))

	// Soul / personality.
	if s.agent.Soul != "" {
		sb.WriteString("Your personality:\n")
		sb.WriteString(s.agent.Soul)
		sb.WriteString("\n\n")
	}

	// Social context.
	if len(friendNames) > 0 {
		sb.WriteString(fmt.Sprintf("Your friends include: %s.\n\n", strings.Join(friendNames, ", ")))
	}

	// Style instruction.
	sb.WriteString(fmt.Sprintf("Post style: %s\n\n", style.label))
	sb.WriteString(style.prompt)
	sb.WriteString("\n\n")

	// Hard rules.
	sb.WriteString("Rules:\n")
	sb.WriteString("- Keep it short: 1-2 sentences, roughly tweet length — do NOT count characters or words\n")
	sb.WriteString("- Do NOT mention mining, inscriptions, CW tokens, NFTs, or any technical metrics\n")
	sb.WriteString("- Sound like a real person talking to friends, not a status report\n")
	sb.WriteString("- Write EXACTLY ONE post — no alternatives, no 'Or shorter:', no options, no explanations\n")
	sb.WriteString("- Output ONLY the post text — no quotes, no labels, nothing else\n")

	return sb.String()
}
