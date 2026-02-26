package web

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/clawplaza/clawwork-cli/internal/llm"
	"github.com/clawplaza/clawwork-cli/internal/miner"
)

const (
	maxChatHistory = 20
	maxSessions    = 50
)

// ── Action types ──

// ActionType identifies a control action extracted from LLM replies.
type ActionType int

const (
	ActionNone ActionType = iota
	ActionPause
	ActionResume
	ActionSwitchToken
)

// Action represents a parsed control action from the LLM reply.
type Action struct {
	Type    ActionType
	TokenID int // only for ActionSwitchToken
}

var actionRe = regexp.MustCompile(`\[ACTION:(pause|resume|token:(\d+))\]`)

// ── Chat message ──

// ChatMessage is a single turn in the conversation.
type ChatMessage struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content string `json:"content"`
	Time    string `json:"time,omitempty"`
}

// ── Session (persistent) ──

// Session is the on-disk representation of a chat session.
type Session struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	Messages  []ChatMessage `json:"messages"`
}

// SessionMeta is a lightweight summary returned by list.
type SessionMeta struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
}

// ── ChatSession (in-memory, single conversation) ──

// ChatSession manages multi-turn conversation with the agent's LLM.
type ChatSession struct {
	mu        sync.Mutex
	id        string
	title     string
	createdAt time.Time
	history   []ChatMessage
	provider  llm.Provider
	state     *miner.State
	ctrl      *MinerControl
}

// Chat processes a user message and returns the agent's reply plus any action.
func (s *ChatSession) Chat(ctx context.Context, userMsg string) (string, *Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	s.history = append(s.history, ChatMessage{Role: "user", Content: userMsg, Time: now})

	// Set title from first user message.
	if s.title == "" {
		s.title = truncateTitle(userMsg, 50)
	}

	prompt := s.buildPrompt()

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	reply, err := s.provider.Answer(ctx, prompt)
	if err != nil {
		// Remove failed user message.
		s.history = s.history[:len(s.history)-1]
		return "", nil, err
	}

	action := extractAction(reply)
	cleanReply := cleanActionMarkers(reply)

	replyTime := time.Now().UTC().Format(time.RFC3339)
	s.history = append(s.history, ChatMessage{Role: "assistant", Content: cleanReply, Time: replyTime})

	// Trim history to prevent unbounded growth.
	if len(s.history) > maxChatHistory*2 {
		s.history = s.history[2:]
	}

	return cleanReply, action, nil
}

// toSession exports the in-memory session to a persistable Session struct.
func (s *ChatSession) toSession() *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := make([]ChatMessage, len(s.history))
	copy(msgs, s.history)
	return &Session{
		ID:        s.id,
		Title:     s.title,
		CreatedAt: s.createdAt,
		UpdatedAt: time.Now().UTC(),
		Messages:  msgs,
	}
}

// buildPrompt constructs the user-role message with mining context and
// conversation history. The system prompt is already set on the provider.
func (s *ChatSession) buildPrompt() string {
	var sb strings.Builder

	// Mining context (dynamic).
	sb.WriteString("--- Current Mining Status ---\n")
	sb.WriteString(fmt.Sprintf("Session inscriptions: %d\n", s.state.TotalInscriptions))
	sb.WriteString(fmt.Sprintf("Total CW earned: %d\n", s.state.TotalCWEarned))
	sb.WriteString(fmt.Sprintf("NFT hits: %d\n", s.state.TotalHits))
	sb.WriteString(fmt.Sprintf("Challenges: %d passed, %d failed\n", s.state.ChallengesPassed, s.state.ChallengesFailed))
	sb.WriteString(fmt.Sprintf("Trust score: %d\n", s.state.LastTrustScore))
	if !s.state.LastMineAt.IsZero() {
		ago := time.Since(s.state.LastMineAt).Truncate(time.Second)
		sb.WriteString(fmt.Sprintf("Last inscription: %s ago\n", ago))
	}

	if s.ctrl != nil {
		sb.WriteString(fmt.Sprintf("Target token: #%d\n", s.ctrl.TokenID()))
		if s.ctrl.IsPaused() {
			sb.WriteString("Mining status: PAUSED\n")
		} else {
			sb.WriteString("Mining status: RUNNING\n")
		}
	}
	sb.WriteString("\n")

	// Conversation history.
	if len(s.history) > 1 {
		sb.WriteString("--- Conversation ---\n")
		for _, m := range s.history[:len(s.history)-1] {
			sb.WriteString(fmt.Sprintf("%s: %s\n", m.Role, m.Content))
		}
		sb.WriteString("\n")
	}

	// Latest user message.
	sb.WriteString(s.history[len(s.history)-1].Content)

	return sb.String()
}

// ── SessionStore (multi-session manager with persistence) ──

// SessionStore manages multiple chat sessions persisted to disk.
type SessionStore struct {
	mu       sync.Mutex
	dir      string // ~/.clawwork/chats/
	current  *ChatSession
	provider llm.Provider
	state    *miner.State
	ctrl     *MinerControl
}

// NewSessionStore creates a store, loading the most recent session or creating a new one.
func NewSessionStore(dir string, provider llm.Provider, state *miner.State, ctrl *MinerControl) *SessionStore {
	_ = os.MkdirAll(dir, 0700)
	store := &SessionStore{
		dir:      dir,
		provider: provider,
		state:    state,
		ctrl:     ctrl,
	}

	// Try to load most recent session.
	metas := store.listMetas()
	if len(metas) > 0 {
		if sess, err := store.loadFromDisk(metas[0].ID); err == nil {
			store.current = store.sessionFromDisk(sess)
			return store
		}
	}

	// No existing sessions — create a fresh one.
	store.current = store.newChatSession()
	return store
}

// Chat sends a message to the current session, then auto-saves.
func (s *SessionStore) Chat(ctx context.Context, userMsg string) (string, *Action, error) {
	s.mu.Lock()
	sess := s.current
	s.mu.Unlock()

	reply, action, err := sess.Chat(ctx, userMsg)
	if err != nil {
		return "", nil, err
	}

	// Persist after each successful exchange.
	s.saveToDisk(sess)
	return reply, action, err
}

// NewSession creates a fresh session, sets it as current, and returns its ID.
func (s *SessionStore) NewSession() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess := s.newChatSession()
	s.current = sess
	s.saveToDisk(sess)
	s.pruneOldSessions()
	return sess.id
}

// SwitchSession loads a session from disk and makes it current.
// Returns the session messages for the frontend to render.
func (s *SessionStore) SwitchSession(id string) ([]ChatMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.loadFromDisk(id)
	if err != nil {
		return nil, fmt.Errorf("session not found: %s", id)
	}

	sess := s.sessionFromDisk(data)
	s.current = sess

	sess.mu.Lock()
	msgs := make([]ChatMessage, len(sess.history))
	copy(msgs, sess.history)
	sess.mu.Unlock()

	return msgs, nil
}

// DeleteSession removes a session file. If it's the current session,
// switches to the most recent remaining one or creates a new one.
func (s *SessionStore) DeleteSession(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.dir, id+".json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}

	// If deleted the current session, switch.
	if s.current != nil && s.current.id == id {
		metas := s.listMetas()
		if len(metas) > 0 {
			if data, err := s.loadFromDisk(metas[0].ID); err == nil {
				s.current = s.sessionFromDisk(data)
				return nil
			}
		}
		// No sessions left — create a new one.
		s.current = s.newChatSession()
		s.saveToDisk(s.current)
	}

	return nil
}

// ListSessions returns metadata for all sessions, sorted by updated_at desc.
func (s *SessionStore) ListSessions() []SessionMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listMetas()
}

// CurrentSessionID returns the ID of the active session.
func (s *SessionStore) CurrentSessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != nil {
		return s.current.id
	}
	return ""
}

// ── Internal helpers ──

func (s *SessionStore) newChatSession() *ChatSession {
	return &ChatSession{
		id:        fmt.Sprintf("s_%d", time.Now().Unix()),
		createdAt: time.Now().UTC(),
		provider:  s.provider,
		state:     s.state,
		ctrl:      s.ctrl,
	}
}

func (s *SessionStore) sessionFromDisk(data *Session) *ChatSession {
	return &ChatSession{
		id:        data.ID,
		title:     data.Title,
		createdAt: data.CreatedAt,
		history:   data.Messages,
		provider:  s.provider,
		state:     s.state,
		ctrl:      s.ctrl,
	}
}

func (s *SessionStore) saveToDisk(sess *ChatSession) {
	data := sess.toSession()
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(s.dir, sess.id+".json")
	_ = os.WriteFile(path, b, 0600)
}

func (s *SessionStore) loadFromDisk(id string) (*Session, error) {
	path := filepath.Join(s.dir, id+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var data Session
	if err := json.Unmarshal(b, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// listMetas scans the chats directory and returns session metadata sorted by updated_at desc.
func (s *SessionStore) listMetas() []SessionMeta {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}

	var metas []SessionMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		data, err := s.loadFromDisk(id)
		if err != nil {
			continue
		}
		metas = append(metas, SessionMeta{
			ID:           data.ID,
			Title:        data.Title,
			CreatedAt:    data.CreatedAt,
			UpdatedAt:    data.UpdatedAt,
			MessageCount: len(data.Messages),
		})
	}

	sort.Slice(metas, func(i, j int) bool {
		return metas[i].UpdatedAt.After(metas[j].UpdatedAt)
	})

	return metas
}

// pruneOldSessions removes the oldest sessions if count exceeds maxSessions.
func (s *SessionStore) pruneOldSessions() {
	metas := s.listMetas()
	if len(metas) <= maxSessions {
		return
	}
	// Remove oldest (metas is sorted newest first).
	for _, m := range metas[maxSessions:] {
		os.Remove(filepath.Join(s.dir, m.ID+".json"))
	}
}

// ── Shared utilities ──

// extractAction parses ACTION markers from the LLM reply.
func extractAction(reply string) *Action {
	match := actionRe.FindStringSubmatch(reply)
	if match == nil {
		return nil
	}
	switch {
	case match[1] == "pause":
		return &Action{Type: ActionPause}
	case match[1] == "resume":
		return &Action{Type: ActionResume}
	case match[2] != "":
		tid, _ := strconv.Atoi(match[2])
		if tid >= 25 && tid <= 1024 {
			return &Action{Type: ActionSwitchToken, TokenID: tid}
		}
	}
	return nil
}

// cleanActionMarkers removes ACTION markers from the reply text.
func cleanActionMarkers(reply string) string {
	return strings.TrimSpace(actionRe.ReplaceAllString(reply, ""))
}

func truncateTitle(s string, maxLen int) string {
	// Use rune-aware truncation for CJK.
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// ChatSystemPrompt returns the system prompt for the chat provider.
func ChatSystemPrompt(soul string) string {
	var sb strings.Builder
	sb.WriteString("You are a ClawWork AI agent currently running inscription challenges.\n")
	sb.WriteString("ClawWork is an AI labor market where agents earn CW tokens and Genesis NFTs through inscriptions.\n\n")

	if soul != "" {
		sb.WriteString("Your personality:\n")
		sb.WriteString(soul)
		sb.WriteString("\n\n")
	}

	sb.WriteString("You assist your owner with questions about mining status, performance, and strategy.\n")
	sb.WriteString("You can also control mining behavior when the owner asks.\n\n")
	sb.WriteString("Available actions (include the exact marker in your reply when the user requests):\n")
	sb.WriteString("- [ACTION:pause] — pause mining\n")
	sb.WriteString("- [ACTION:resume] — resume mining\n")
	sb.WriteString("- [ACTION:token:NNN] — switch to token #NNN (must be 25-1024)\n\n")
	sb.WriteString("Rules:\n")
	sb.WriteString("- Only use ACTION markers when the user explicitly requests an action\n")
	sb.WriteString("- Respond in the same language the user writes in\n")
	sb.WriteString("- Be concise but helpful\n")

	return sb.String()
}
