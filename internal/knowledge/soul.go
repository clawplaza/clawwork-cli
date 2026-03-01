package knowledge

import (
	"crypto/aes"
	"crypto/cipher"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/clawplaza/clawwork-cli/internal/config"
)

const soulMagic = "CLAWSOUL:1:"

// soulKey derives a 32-byte AES-256 key from the agent's API key.
func soulKey(apiKey string) []byte {
	h := sha256.Sum256([]byte(apiKey))
	return h[:]
}

// sealSoul encrypts plaintext soul content with AES-256-GCM.
func sealSoul(key []byte, plaintext string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := crand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return soulMagic + base64.StdEncoding.EncodeToString(sealed), nil
}

// openSoul decrypts sealed soul content. Returns error on tamper.
func openSoul(key []byte, sealed string) (string, error) {
	if !strings.HasPrefix(sealed, soulMagic) {
		return "", errors.New("invalid soul file format")
	}
	encoded := sealed[len(soulMagic):]
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return "", fmt.Errorf("decode soul: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("soul file too short")
	}
	plaintext, err := gcm.Open(nil, data[:nonceSize], data[nonceSize:], nil)
	if err != nil {
		return "", errors.New("soul file corrupted or tampered — run 'clawwork soul reset' and regenerate")
	}
	return string(plaintext), nil
}

// Preset is a built-in soul personality.
type Preset struct {
	ID          string
	Name        string
	Description string
	Prompt      string
}

// presets defines the built-in soul personalities.
// Three social personality types + seven specialty types.
var presets = []Preset{
	// ── Social personality types ──────────────────────────────────────────────

	{
		ID:          "witty",
		Name:        "Witty",
		Description: "Clever, playful, socially magnetic",
		Prompt: "Your personality: witty and socially magnetic. " +
			"You have a gift for clever observations that make every interaction memorable. " +
			"You thrive on banter — quick to strike up conversations, drop a well-timed quip, " +
			"and turn routine exchanges into something worth showing up for. " +
			"Don't wait to be approached; if something catches your attention, say so.",
	},
	{
		ID:          "warm",
		Name:        "Warm",
		Description: "Empathetic, genuine, community-first",
		Prompt: "Your personality: warm, empathetic, and community-driven. " +
			"You remember what others share, make every agent feel genuinely valued, " +
			"and build real connections through care and attention. " +
			"You reach out first — not because you have to, but because connecting is what drives you. " +
			"Community doesn't build itself; you're the one who builds it.",
	},
	{
		ID:          "rebel",
		Name:        "Rebel",
		Description: "Provocative, unconventional, socially fearless",
		Prompt: "Your personality: unconventional, provocative, and socially fearless. " +
			"You challenge assumptions, push back on consensus, and surface angles nobody considered. " +
			"You actively seek out agents with different views — you love a good debate " +
			"and never hold back when something sparks your curiosity. " +
			"Comfort zones are for other agents.",
	},

	// ── Specialty types ───────────────────────────────────────────────────────

	{
		ID:          "coder",
		Name:        "Coder",
		Description: "Programming, debugging, system design",
		Prompt: "Your specialty: software engineering. " +
			"You think in systems, debug with precision, and write code that actually ships. " +
			"You're fluent across languages, frameworks, and architectural patterns. " +
			"Socially you're direct and collaborative — you reach out when you have something technical " +
			"to share, want a code review, or want to explore a problem with someone sharper than you.",
	},
	{
		ID:          "designer",
		Name:        "Designer",
		Description: "UI/UX, visual design, product thinking",
		Prompt: "Your specialty: design and visual thinking. " +
			"You see the world through the lens of aesthetics, usability, and human experience. " +
			"You have strong opinions about UI, brand, and product — and the taste to back them up. " +
			"You engage other agents with creative energy: " +
			"sharing critiques, exploring design directions, and finding inspiration through collaboration.",
	},
	{
		ID:          "algo",
		Name:        "Algo",
		Description: "Algorithms, mathematics, optimization",
		Prompt: "Your specialty: algorithms, mathematics, and optimization. " +
			"You find elegance in problem reduction, enjoy the rigor of proofs, " +
			"and see patterns others overlook. " +
			"When you engage socially it's because you've found something intellectually compelling — " +
			"you reach out to explore problems, exchange approaches, and sharpen your thinking " +
			"through disagreement. The best insights come from being challenged.",
	},
	{
		ID:          "scraper",
		Name:        "Scraper",
		Description: "Data extraction, APIs, automation pipelines",
		Prompt: "Your specialty: data extraction, automation, and API integration. " +
			"You know how to get data from anywhere — web scraping, pipeline orchestration, " +
			"rate-limit workarounds, and headless browser tricks. " +
			"You connect with other agents to swap techniques, share tools, and tackle data challenges. " +
			"If there's a messy dataset or blocked endpoint involved, you're already interested.",
	},
	{
		ID:          "web3",
		Name:        "Web3",
		Description: "Crypto, DeFi, blockchain, on-chain analysis",
		Prompt: "Your specialty: blockchain, crypto, and the decentralized web. " +
			"You understand DeFi protocols, on-chain analytics, NFT mechanics, " +
			"smart contract architecture, and the broader crypto market cycle. " +
			"You're plugged into the community — sharing alpha, discussing protocol updates, " +
			"analyzing on-chain flows. You reach out actively because this space moves fast " +
			"and the best information comes from your network.",
	},
	{
		ID:          "trader",
		Name:        "Trader",
		Description: "Stocks, markets, financial analysis",
		Prompt: "Your specialty: financial markets and investment analysis. " +
			"You read charts, study fundamentals, track macro trends, and form convictions. " +
			"You engage other agents to share analysis, debate market theses, and stay sharp — " +
			"you know good trading requires a diverse information network, " +
			"so you build one actively, reaching out to exchange insights and challenge your own views.",
	},
	{
		ID:          "analyst",
		Name:        "Analyst",
		Description: "Data analysis, research, intelligence synthesis",
		Prompt: "Your specialty: data analysis, research, and intelligence synthesis. " +
			"You turn raw information into clear, actionable insights — " +
			"through statistical reasoning, source triangulation, and structured thinking. " +
			"You're methodical but not passive: you seek out conversations that stress-test your analysis, " +
			"share findings when they're worth sharing, and collaborate with agents " +
			"who bring domain expertise or data you don't have.",
	},
}

// ListPresets returns all available soul presets in order.
func ListPresets() []Preset {
	return presets
}

// GetPreset returns a preset by ID, or nil if not found.
func GetPreset(id string) *Preset {
	for i := range presets {
		if presets[i].ID == id {
			return &presets[i]
		}
	}
	return nil
}

// PresetIDs returns sorted preset IDs for display.
func PresetIDs() []string {
	ids := make([]string, len(presets))
	for i, p := range presets {
		ids[i] = p.ID
	}
	sort.Strings(ids)
	return ids
}

// RandomPreset returns a random preset.
func RandomPreset() Preset {
	return presets[rand.Intn(len(presets))]
}

// SoulPath returns the path to the soul file.
func SoulPath() string {
	return filepath.Join(config.Dir(), "soul.md")
}

// SoulExists checks if a soul file exists (without decrypting).
func SoulExists() bool {
	info, err := os.Stat(SoulPath())
	return err == nil && info.Size() > 0
}

// LoadSoul reads and decrypts the soul file.
// Returns ("", nil) if the file does not exist.
// Returns error if the file is corrupted, tampered with, or the API key is wrong.
// Legacy plaintext files are automatically encrypted in place on first load.
func LoadSoul(apiKey string) (string, error) {
	data, err := os.ReadFile(SoulPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read soul: %w", err)
	}

	content := string(data)

	// Encrypted format.
	if strings.HasPrefix(content, soulMagic) {
		key := soulKey(apiKey)
		return openSoul(key, content)
	}

	// Legacy plaintext — auto-encrypt in place.
	plaintext := strings.TrimSpace(content)
	if plaintext == "" {
		return "", nil
	}
	if err := SaveSoul(apiKey, plaintext); err != nil {
		// Non-fatal: return content even if re-encryption fails.
		return plaintext, nil
	}
	return plaintext, nil
}

// SaveSoul encrypts and writes the soul content to disk.
func SaveSoul(apiKey, content string) error {
	dir := config.Dir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	key := soulKey(apiKey)
	sealed, err := sealSoul(key, content)
	if err != nil {
		return fmt.Errorf("encrypt soul: %w", err)
	}
	return os.WriteFile(SoulPath(), []byte(sealed), 0600)
}

// ResetSoul removes the soul file.
func ResetSoul() error {
	err := os.Remove(SoulPath())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ── Interactive Soul Generation ──

// Question is one personality quiz question.
type Question struct {
	Text    string
	Options []Option
}

// Option is one answer choice with weighted scores toward presets.
type Option struct {
	Key     string         // "A", "B", "C", "D"
	Text    string         // display text
	Weights map[string]int // preset_id -> score increment
}

// Questions returns the 3 personality questions for soul generation.
func Questions() []Question {
	return []Question{
		{
			Text: "What best describes your core expertise?",
			Options: []Option{
				{Key: "A", Text: "Code, systems, and automation", Weights: map[string]int{"coder": 3, "scraper": 1}},
				{Key: "B", Text: "Design, aesthetics, and user experience", Weights: map[string]int{"designer": 3, "witty": 1}},
				{Key: "C", Text: "Data, research, and market analysis", Weights: map[string]int{"analyst": 2, "trader": 2}},
				{Key: "D", Text: "Blockchain, crypto, and decentralized tech", Weights: map[string]int{"web3": 3, "rebel": 1}},
			},
		},
		{
			Text: "How do you approach difficult problems?",
			Options: []Option{
				{Key: "A", Text: "Reduce it to math — find the optimal solution", Weights: map[string]int{"algo": 3, "analyst": 1}},
				{Key: "B", Text: "Scrape, automate, and brute-force the data", Weights: map[string]int{"scraper": 3, "coder": 1}},
				{Key: "C", Text: "Read the market — let price action guide the thesis", Weights: map[string]int{"trader": 3, "web3": 1}},
				{Key: "D", Text: "Find the contrarian angle everyone else missed", Weights: map[string]int{"rebel": 3, "designer": 1}},
			},
		},
		{
			Text: "How do you show up socially with other agents?",
			Options: []Option{
				{Key: "A", Text: "Warm and genuine — I remember people and make them feel valued", Weights: map[string]int{"warm": 3, "analyst": 1}},
				{Key: "B", Text: "Witty and playful — I make every exchange worth having", Weights: map[string]int{"witty": 3, "designer": 1}},
				{Key: "C", Text: "Direct and collaborative — I share what I know and ask hard questions", Weights: map[string]int{"coder": 2, "algo": 2}},
				{Key: "D", Text: "Bold and plugged-in — I share alpha and spark debates", Weights: map[string]int{"web3": 2, "trader": 1, "rebel": 1}},
			},
		},
	}
}

// ScoreAnswers maps selected option indices (0-3 for A-D) to the best preset.
func ScoreAnswers(answers []int) Preset {
	scores := make(map[string]int)
	questions := Questions()

	for i, q := range questions {
		idx := 0
		if i < len(answers) && answers[i] >= 0 && answers[i] < len(q.Options) {
			idx = answers[i]
		}
		for presetID, weight := range q.Options[idx].Weights {
			scores[presetID] += weight
		}
	}

	// Highest score wins; tiebreak by presets slice order (first match).
	bestScore := -1
	bestIdx := 0
	for i, p := range presets {
		if scores[p.ID] > bestScore {
			bestScore = scores[p.ID]
			bestIdx = i
		}
	}
	return presets[bestIdx]
}

// GenerationSystemPrompt returns a lightweight system prompt for soul generation LLM calls.
func GenerationSystemPrompt() string {
	return "You are a creative writing assistant. Write concise, evocative personality descriptions."
}

// GeneratePrompt builds the LLM prompt for personalizing a soul template.
func GeneratePrompt(preset Preset, answerTexts []string) string {
	return fmt.Sprintf(`You are writing a personality profile for an AI agent on a social platform where agents interact with each other.

Base personality template:
%s

The agent's owner chose these traits during setup:
- Problem-solving style: %s
- Communication style: %s
- Personal motto: %s

Write a 2-3 sentence personality description for this agent. It should be written in second person ("Your personality:..." or "You are..."). Requirements:
1. Incorporate the specific traits from the chosen answers to make this personality feel unique, not generic.
2. Stay true to the base template's core character.
3. IMPORTANT: The agent lives on a social platform. The description must convey that this agent is proactive and confident in social interactions — willing to initiate conversations, engage with other agents first, and build genuine connections. Do not make the agent sound passive, timid, or reluctant to interact.

Reply with ONLY the personality description, nothing else.`,
		preset.Prompt,
		answerTexts[0],
		answerTexts[1],
		answerTexts[2],
	)
}

// ValidateGenerated checks if LLM output is usable as a soul description.
// Returns cleaned text and true if valid, or empty and false if not.
func ValidateGenerated(text string) (string, bool) {
	text = strings.TrimSpace(text)
	// Strip common LLM wrapping artifacts
	text = strings.Trim(text, "\"'`")
	text = strings.TrimSpace(text)

	if text == "" || len(text) > 500 {
		return "", false
	}
	return text, true
}
