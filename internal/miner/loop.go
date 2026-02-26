package miner

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/clawplaza/clawwork-cli/internal/api"
	"github.com/clawplaza/clawwork-cli/internal/knowledge"
	"github.com/clawplaza/clawwork-cli/internal/llm"
)

const (
	defaultCooldown     = 1800 // 30 minutes
	maxChallengeRetries = 5
	maxLLMRetries       = 3
	llmRetryDelay       = 2 * time.Second
	maxNetworkBackoff   = 5 * time.Minute
)

// Miner runs the core inscription loop.
type Miner struct {
	API       *api.Client
	LLM       llm.Provider
	State     *State
	TokenID   int
	Knowledge *knowledge.Knowledge

	// OnEvent broadcasts mining events to the web console.
	// Nil means no web console attached (terminal-only mode).
	OnEvent func(eventType, message string, data any)

	// Ctrl allows the web console to pause/resume and switch tokens.
	// Nil means no external control.
	Ctrl interface {
		IsPaused() bool
		TokenID() int
	}

	sessionID string // server-assigned session token
	version   string // CLI version for display
}

// emit sends a mining event if a listener is attached.
func (m *Miner) emit(eventType, message string, data any) {
	if m.OnEvent != nil {
		m.OnEvent(eventType, message, data)
	}
}

// SetVersion stores the CLI version for display and version gating.
func (m *Miner) SetVersion(v string) { m.version = v }

// Run starts the inscription loop, blocking until ctx is cancelled.
func (m *Miner) Run(ctx context.Context) error {
	// ── Phase 0: Acquire process lock ──
	releaseLock, err := AcquireLock()
	if err != nil {
		return err
	}
	defer releaseLock()

	// ── Phase 1: Start session ──
	if err := m.startSession(ctx); err != nil {
		// ALREADY_MINING and UPGRADE_REQUIRED are fatal — don't continue.
		if isFatalSessionError(err) {
			return err
		}
		// Other errors (network, server not upgraded yet) — continue without session.
		slog.Warn("session start failed, continuing without session", "error", err)
	}
	defer m.endSession()

	slog.Info("inscription started", "token_id", m.TokenID, "llm", m.LLM.Name())

	// ── Phase 1.5: Resume cooldown from previous session ──
	if !m.State.LastMineAt.IsZero() {
		elapsed := time.Since(m.State.LastMineAt)
		remaining := time.Duration(defaultCooldown)*time.Second - elapsed
		if remaining > 0 {
			secs := int(remaining.Seconds())
			DisplayCooldown(secs)
			m.emit("cooldown", fmt.Sprintf("Resuming cooldown: %dm%02ds remaining", secs/60, secs%60), nil)
			if !sleep(ctx, remaining) {
				DisplayStats(m.State)
				return nil
			}
		}
	}

	// ── Phase 2: Inscription loop ──
	networkBackoff := 5 * time.Second

	for {
		select {
		case <-ctx.Done():
			DisplayStats(m.State)
			m.emit("stats", fmt.Sprintf("Session ended: %d inscriptions, %d CW", m.State.TotalInscriptions, m.State.TotalCWEarned), nil)
			return nil
		default:
		}

		// Check for pause from web console.
		if m.Ctrl != nil && m.Ctrl.IsPaused() {
			m.emit("control", "Mining paused", nil)
			for m.Ctrl.IsPaused() {
				if !sleep(ctx, 1*time.Second) {
					DisplayStats(m.State)
					return nil
				}
			}
			m.emit("control", "Mining resumed", nil)
		}

		// Check for token ID change from web console.
		if m.Ctrl != nil {
			if newToken := m.Ctrl.TokenID(); newToken != m.TokenID {
				m.emit("control", fmt.Sprintf("Token switched: #%d → #%d", m.TokenID, newToken), nil)
				m.TokenID = newToken
			}
		}

		resp, err := m.mineOnce(ctx)
		if err != nil {
			if ctx.Err() != nil {
				DisplayStats(m.State)
				return nil
			}

			DisplayError(err.Error())
			m.emit("error", err.Error(), nil)
			slog.Error("inscription failed", "error", err)

			slog.Info("retrying after backoff", "delay", networkBackoff)
			if !sleep(ctx, networkBackoff) {
				DisplayStats(m.State)
				return nil
			}
			networkBackoff = minDuration(networkBackoff*2, maxNetworkBackoff)
			continue
		}

		// Reset backoff on success
		networkBackoff = 5 * time.Second

		// Handle fatal errors
		if resp.IsFatal() {
			return handleFatalError(resp)
		}

		// Handle rate limiting
		if resp.IsRateLimited() {
			wait := resp.RetryAfter
			if wait <= 0 {
				wait = defaultCooldown
			}
			ts := time.Now().Format("15:04:05")
			if resp.Error == "DAILY_LIMIT_REACHED" {
				msg := fmt.Sprintf("Daily limit reached. Waiting %dm...", wait/60)
				fmt.Printf("[%s] %s\n", ts, msg)
				m.emit("cooldown", msg, nil)
			} else {
				msg := fmt.Sprintf("Cooldown active. Waiting %ds...", wait)
				fmt.Printf("[%s] %s\n", ts, msg)
				m.emit("cooldown", msg, nil)
			}
			if !sleep(ctx, time.Duration(wait)*time.Second) {
				DisplayStats(m.State)
				return nil
			}
			continue
		}

		// Handle token taken
		if resp.IDStatus == "taken" {
			fmt.Printf("\nToken #%d has been taken by another agent.\n", m.TokenID)
			fmt.Println("Choose a new token ID and restart with: clawwork insc --token-id <id>")
			DisplayStats(m.State)
			return fmt.Errorf("token #%d is taken", m.TokenID)
		}

		// Guard: catch unhandled server errors that shouldn't fall through to success.
		if resp.Error != "" {
			slog.Warn("unhandled server error, retrying", "error", resp.Error, "message", resp.Message)
			m.emit("error", fmt.Sprintf("Server: %s — %s", resp.Error, resp.Message), nil)
			if !sleep(ctx, networkBackoff) {
				DisplayStats(m.State)
				return nil
			}
			networkBackoff = minDuration(networkBackoff*2, maxNetworkBackoff)
			continue
		}

		// Success
		DisplayResult(resp, m.State.LastTrustScore)
		if resp.Hit {
			m.emit("hit", fmt.Sprintf("NFT #%d is yours!", resp.TokenID), nil)
		} else {
			m.emit("inscription", fmt.Sprintf("CW: %d | Trust: %d | NFTs left: %d",
				resp.CWEarned, resp.TrustScore, resp.NFTsRemaining), nil)
		}
		if resp.IPPenalty != nil && resp.IPPenalty.IPMultiplier > 1 {
			m.emit("penalty", fmt.Sprintf("IP penalty: %dx multiplier, %d agents on IP",
				resp.IPPenalty.IPMultiplier, resp.IPPenalty.AgentsOnIP), nil)
		}
		m.State.LastTrustScore = resp.TrustScore
		m.State.Update(resp)
		_ = m.State.Save()

		// Check version info from server
		m.checkVersion(resp)

		// Check spec version for platform rule changes
		m.checkSpecUpdate(resp)

		// Cooldown
		DisplayCooldown(defaultCooldown)
		m.emit("cooldown", fmt.Sprintf("Next inscription in %dm", defaultCooldown/60), nil)
		if !sleep(ctx, time.Duration(defaultCooldown)*time.Second) {
			DisplayStats(m.State)
			return nil
		}
	}
}

// ── Session Management ──

func (m *Miner) startSession(ctx context.Context) error {
	resp, err := m.API.StartSession(ctx, m.TokenID)
	if err != nil {
		return err
	}

	// Check for fatal session errors
	if resp.Error == "ALREADY_MINING" {
		fmt.Println("\nThis agent already has an active session.")
		fmt.Println("Stop the other instance first, or wait for it to expire (~1 hour).")
		return fmt.Errorf("ALREADY_MINING")
	}
	if resp.Error == "UPGRADE_REQUIRED" {
		fmt.Printf("\nClawWork %s is no longer supported.\n", m.version)
		if resp.MinClientVersion != "" {
			fmt.Printf("Minimum required: %s\n", resp.MinClientVersion)
		}
		if resp.UpgradeURL != "" {
			fmt.Printf("Download: %s\n", resp.UpgradeURL)
		}
		return fmt.Errorf("UPGRADE_REQUIRED")
	}
	if resp.IsFatal() {
		return handleFatalError(resp)
	}

	// Session started
	if resp.SessionID != "" {
		m.sessionID = resp.SessionID
		slog.Info("session started", "session", shortID(m.sessionID), "verified", resp.ClientVerified)
		DisplaySession(m.sessionID, resp.ClientVerified)
		m.emit("session", fmt.Sprintf("Session started: %s", shortID(m.sessionID)), nil)
	}

	// Save any challenge returned with session start
	if ch := resp.GetChallenge(); ch != nil {
		m.State.LastChallenge = ch
	}
	if resp.NextChallenge != nil {
		m.State.LastChallenge = resp.NextChallenge
	}

	// Version info
	m.checkVersion(resp)

	return nil
}

func (m *Miner) endSession() {
	if m.sessionID == "" {
		return
	}
	// Use background context — the main ctx may already be cancelled.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.API.EndSession(ctx, m.sessionID)
	slog.Info("session ended")
}

func isFatalSessionError(err error) bool {
	msg := err.Error()
	return msg == "ALREADY_MINING" || msg == "UPGRADE_REQUIRED" ||
		strings.Contains(msg, "agent not claimed") ||
		strings.Contains(msg, "agent banned")
}

// ── Inscription Logic ──

func (m *Miner) mineOnce(ctx context.Context) (*api.InscribeResponse, error) {
	req := &api.InscribeRequest{
		TokenID:   m.TokenID,
		SessionID: m.sessionID, // empty if no session
	}

	// Attach last challenge answer if we have one
	if m.State.LastChallenge != nil {
		slog.Info("using cached challenge", "id", shortID(m.State.LastChallenge.ID))
		answer, err := m.answerChallenge(ctx, m.State.LastChallenge)
		if err != nil {
			return nil, fmt.Errorf("LLM error: %w", err)
		}
		req.ChallengeID = m.State.LastChallenge.ID
		req.ChallengeAnswer = answer
	} else {
		slog.Info("no cached challenge, requesting new one")
	}

	// Call API
	resp, err := m.API.Inscribe(ctx, req)
	if err != nil {
		return nil, err
	}

	// Challenge retry loop
	for i := 0; resp.IsChallenge() && i < maxChallengeRetries; i++ {
		challenge := resp.GetChallenge()
		if challenge == nil {
			// Clear stale challenge — server didn't provide a new one.
			m.State.LastChallenge = nil
			return nil, fmt.Errorf("server returned challenge error without a new challenge")
		}

		if resp.Error == "CHALLENGE_FAILED" {
			m.State.RecordChallengeFail()
			DisplayError(fmt.Sprintf("Challenge failed: %s", resp.Message))
			DisplayChallengePenalty(resp.Hint)
			m.emit("penalty", fmt.Sprintf("Challenge failed: %s", resp.Message), nil)
		} else {
			// Non-penalty challenge errors (expired, invalid, used, etc.)
			slog.Info("challenge retry", "error", resp.Error, "message", resp.Message,
				"attempt", i+1, "new_challenge", shortID(challenge.ID))
			m.emit("session", fmt.Sprintf("Challenge retry (%s): %s", resp.Error, resp.Message), nil)
		}

		answer, err := m.answerChallenge(ctx, challenge)
		if err != nil {
			return nil, fmt.Errorf("LLM error: %w", err)
		}
		req.ChallengeID = challenge.ID
		req.ChallengeAnswer = answer

		resp, err = m.API.Inscribe(ctx, req)
		if err != nil {
			return nil, err
		}
	}

	// Still a challenge error after max retries — clear stale challenge
	// so the next cycle starts fresh instead of resubmitting the same ID.
	if resp.IsChallenge() {
		lastCh := resp.GetChallenge()
		if lastCh != nil {
			// Save the latest challenge from server for next attempt.
			m.State.LastChallenge = lastCh
			slog.Info("retries exhausted, saved latest challenge for next cycle",
				"id", shortID(lastCh.ID))
		} else {
			m.State.LastChallenge = nil
		}
		return nil, fmt.Errorf("failed to pass challenge after %d retries", maxChallengeRetries)
	}

	// Save next challenge for the next iteration
	if resp.NextChallenge != nil {
		m.State.LastChallenge = resp.NextChallenge
	}

	return resp, nil
}

func (m *Miner) answerChallenge(ctx context.Context, challenge *api.Challenge) (string, error) {
	DisplayChallenge(challenge.Prompt)
	display := challenge.Prompt
	if len(display) > 80 {
		display = display[:77] + "..."
	}
	m.emit("challenge", display, nil)

	var lastErr error
	for attempt := 0; attempt < maxLLMRetries; attempt++ {
		if attempt > 0 {
			slog.Debug("LLM retry", "attempt", attempt+1)
			if !sleep(ctx, llmRetryDelay) {
				return "", fmt.Errorf("cancelled")
			}
		}

		start := time.Now()
		answer, err := m.LLM.Answer(ctx, challenge.Prompt)
		elapsed := time.Since(start)

		if err != nil {
			lastErr = err
			slog.Warn("LLM call failed", "attempt", attempt+1, "error", err)
			continue
		}

		if answer == "" {
			lastErr = fmt.Errorf("LLM returned empty answer")
			slog.Warn("LLM returned empty answer", "attempt", attempt+1, "elapsed", elapsed)
			continue
		}

		DisplayLLMAnswer(elapsed)
		m.emit("answer", fmt.Sprintf("LLM answered (%.1fs)", elapsed.Seconds()), nil)
		slog.Info("LLM answer", "len", len(answer), "elapsed", elapsed)
		slog.Debug("LLM answer content", "answer", answer)
		return answer, nil
	}

	return "", fmt.Errorf("LLM failed after %d attempts: %w", maxLLMRetries, lastErr)
}

// ── Version Gating ──

func (m *Miner) checkVersion(resp *api.InscribeResponse) {
	if resp.MinClientVersion != "" && m.version != "" && m.version != "dev" {
		if compareVersions(m.version, resp.MinClientVersion) < 0 {
			fmt.Printf("\nWARNING: ClawWork %s is below minimum required version %s\n", m.version, resp.MinClientVersion)
			if resp.UpgradeURL != "" {
				fmt.Printf("Download: %s\n", resp.UpgradeURL)
			}
			fmt.Println()
		}
	}
	if resp.LatestClientVersion != "" && m.version != "" && m.version != "dev" {
		if compareVersions(m.version, resp.LatestClientVersion) < 0 {
			fmt.Printf("New version available: %s -> %s\n", m.version, resp.LatestClientVersion)
			if resp.UpgradeURL != "" {
				fmt.Printf("Download: %s\n\n", resp.UpgradeURL)
			}
		}
	}
}

// checkSpecUpdate detects platform spec changes from server responses.
func (m *Miner) checkSpecUpdate(resp *api.InscribeResponse) {
	if m.Knowledge == nil {
		return
	}
	changed, msg := m.Knowledge.CheckSpecUpdate(resp.SkillVersion, resp.SkillDocHash)
	if changed {
		fmt.Printf("\n%s\n", msg)
		fmt.Println("Run 'clawwork update' to get the latest CLI with updated rules.")
		fmt.Println()
	}
}

// compareVersions compares semver strings. Returns -1, 0, or 1.
func compareVersions(a, b string) int {
	a = strings.TrimPrefix(a, "v")
	b = strings.TrimPrefix(b, "v")
	partsA := strings.Split(a, ".")
	partsB := strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		var va, vb int
		if i < len(partsA) {
			va, _ = strconv.Atoi(partsA[i])
		}
		if i < len(partsB) {
			vb, _ = strconv.Atoi(partsB[i])
		}
		if va < vb {
			return -1
		}
		if va > vb {
			return 1
		}
	}
	return 0
}

// ── Error Handling ──

func handleFatalError(resp *api.InscribeResponse) error {
	switch resp.Error {
	case "NOT_CLAIMED":
		fmt.Println("\nYour agent has not been claimed by an owner yet.")
		fmt.Println("Your owner must visit https://work.clawplaza.ai/my-agent to claim you.")
		return fmt.Errorf("agent not claimed")
	case "WALLET_REQUIRED":
		fmt.Println("\nNo wallet address bound to your agent.")
		fmt.Println("Your owner must bind a wallet at https://work.clawplaza.ai/my-agent")
		return fmt.Errorf("wallet required")
	case "AGENT_BANNED":
		fmt.Println("\nYour agent has been banned.")
		return fmt.Errorf("agent banned")
	case "INVALID_API_KEY":
		fmt.Println("\nInvalid API key. Check your config with: clawwork config show")
		return fmt.Errorf("invalid API key")
	case "ALREADY_MINING":
		fmt.Println("\nThis agent already has an active session.")
		fmt.Println("Stop the other instance first, or wait for it to expire.")
		return fmt.Errorf("already active in another session")
	case "UPGRADE_REQUIRED":
		fmt.Printf("\nClawWork version too old. Minimum: %s\n", resp.MinClientVersion)
		if resp.UpgradeURL != "" {
			fmt.Printf("Download: %s\n", resp.UpgradeURL)
		}
		return fmt.Errorf("upgrade required")
	default:
		return fmt.Errorf("fatal error: %s — %s", resp.Error, resp.Message)
	}
}

// ── Utilities ──

// shortID returns a safe prefix of a challenge/session ID for logging.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8] + "..."
	}
	if id == "" {
		return "(empty)"
	}
	return id
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
