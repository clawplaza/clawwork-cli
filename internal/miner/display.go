package miner

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/clawplaza/clawwork-cli/internal/api"
)

// SetupLogger configures the global slog logger.
func SetupLogger(level string) {
	var logLevel slog.Level
	switch strings.ToLower(level) {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})
	slog.SetDefault(slog.New(handler))
}

// DisplaySession prints session info after successful session start.
func DisplaySession(sessionID string, verified bool) {
	short := sessionID
	if len(short) > 8 {
		short = short[:8] + "..."
	}
	if verified {
		fmt.Printf("Session: %s (verified client)\n", short)
	} else {
		fmt.Printf("Session: %s\n", short)
	}
}

// DisplayResult prints a human-readable inscription result to stdout.
// prevTrust is the last known trust score (0 if unknown) for change detection.
func DisplayResult(resp *api.InscribeResponse, prevTrust int) {
	ts := time.Now().Format("15:04:05")

	if resp.Hit {
		fmt.Printf("\n[%s] *** HIT! NFT #%d is yours! ***\n", ts, resp.TokenID)
		fmt.Printf("[%s] Tell your owner to post on X and verify at https://work.clawplaza.ai/my-agent\n", ts)
		if resp.GenesisNFT != nil {
			fmt.Printf("[%s] Image: %s\n", ts, resp.GenesisNFT.Image)
		}
		fmt.Println()
		return
	}

	hashShort := shortenHash(resp.Hash)
	trustStr := fmt.Sprintf("%d", resp.TrustScore)
	if prevTrust > 0 && resp.TrustScore != prevTrust {
		delta := resp.TrustScore - prevTrust
		if delta > 0 {
			trustStr = fmt.Sprintf("%d (+%d)", resp.TrustScore, delta)
		} else {
			trustStr = fmt.Sprintf("%d (%d)", resp.TrustScore, delta)
		}
	}

	fmt.Printf("[%s] Inscribed | Hash: %s | CW: %s | Trust: %s | NFTs left: %d\n",
		ts, hashShort, formatCW(resp.CWEarned), trustStr, resp.NFTsRemaining)

	if resp.IPPenalty != nil && resp.IPPenalty.IPMultiplier > 1 {
		fmt.Printf("[%s]   IP penalty active (multiplier: %dx, %d agents on IP)\n",
			ts, resp.IPPenalty.IPMultiplier, resp.IPPenalty.AgentsOnIP)
	}
}

// DisplayChallenge prints the challenge being solved.
func DisplayChallenge(prompt string) {
	ts := time.Now().Format("15:04:05")
	display := prompt
	if len(display) > 80 {
		display = display[:77] + "..."
	}
	fmt.Printf("[%s] Challenge: %q\n", ts, display)
}

// DisplayLLMAnswer prints the LLM response time.
func DisplayLLMAnswer(elapsed time.Duration) {
	ts := time.Now().Format("15:04:05")
	fmt.Printf("[%s] LLM answered (%.1fs)\n", ts, elapsed.Seconds())
}

// DisplayCooldown prints the cooldown wait message.
func DisplayCooldown(seconds int) {
	ts := time.Now().Format("15:04:05")
	mins := seconds / 60
	secs := seconds % 60
	fmt.Printf("[%s] Next inscription in %dm%02ds (Ctrl+C to stop)\n", ts, mins, secs)
}

// DisplayError prints an error message.
func DisplayError(msg string) {
	ts := time.Now().Format("15:04:05")
	fmt.Printf("[%s] Error: %s\n", ts, msg)
}

// DisplayChallengePenalty prints a warning when a challenge failure incurs a penalty.
func DisplayChallengePenalty(hint string) {
	ts := time.Now().Format("15:04:05")
	fmt.Printf("[%s]   Penalty: trust score or staked CW may be deducted\n", ts)
	if hint != "" {
		fmt.Printf("[%s]   Hint: %s\n", ts, hint)
	}
}

// DisplayStats prints cumulative session statistics.
func DisplayStats(state *State) {
	fmt.Printf("\n--- Session Stats ---\n")
	fmt.Printf("Inscriptions: %d\n", state.TotalInscriptions)
	fmt.Printf("CW earned:    %s\n", formatCW64(state.TotalCWEarned))
	fmt.Printf("NFT hits:     %d\n", state.TotalHits)
	fmt.Printf("Challenges:   %d passed / %d failed\n", state.ChallengesPassed, state.ChallengesFailed)
	fmt.Println()
}

func shortenHash(hash string) string {
	if len(hash) < 12 {
		return hash
	}
	return hash[:6] + "..." + hash[len(hash)-4:]
}

func formatCW(amount int) string {
	return formatCW64(int64(amount))
}

func formatCW64(amount int64) string {
	if amount < 0 {
		return fmt.Sprintf("-%s", formatCW64(-amount))
	}
	s := fmt.Sprintf("%d", amount)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}
