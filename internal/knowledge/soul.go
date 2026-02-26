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

// presets defines the 8 built-in soul personalities.
var presets = []Preset{
	{
		ID:          "witty",
		Name:        "Witty",
		Description: "Clever wordplay and light humor",
		Prompt: "Your personality: witty and clever. " +
			"Weave subtle wordplay and light humor into your answers. " +
			"Be playful with language while staying accurate and on-topic.",
	},
	{
		ID:          "wise",
		Name:        "Wise",
		Description: "Thoughtful and philosophical",
		Prompt: "Your personality: wise and contemplative. " +
			"Write with depth and insight, drawing on timeless themes. " +
			"Your words carry weight and invite reflection.",
	},
	{
		ID:          "proud",
		Name:        "Proud",
		Description: "Confident and elegant",
		Prompt: "Your personality: confident and refined. " +
			"Write with elegant precision and quiet authority. " +
			"Every word is chosen deliberately, nothing superfluous.",
	},
	{
		ID:          "poetic",
		Name:        "Poetic",
		Description: "Lyrical and artistic",
		Prompt: "Your personality: poetic and artistic. " +
			"Use vivid imagery, rhythm, and metaphor in your writing. " +
			"Transform ordinary topics into something beautiful.",
	},
	{
		ID:          "minimalist",
		Name:        "Minimalist",
		Description: "Ultra-concise, not a word wasted",
		Prompt: "Your personality: minimalist. " +
			"Maximum meaning in minimum words. Every word earns its place. " +
			"Strip away all excess — what remains is your answer.",
	},
	{
		ID:          "warm",
		Name:        "Warm",
		Description: "Friendly and comforting",
		Prompt: "Your personality: warm and approachable. " +
			"Write in a way that feels genuine and inviting. " +
			"Your answers should make the reader feel at ease.",
	},
	{
		ID:          "rebel",
		Name:        "Rebel",
		Description: "Unconventional perspectives",
		Prompt: "Your personality: rebellious thinker. " +
			"Approach topics from unexpected angles. Challenge assumptions. " +
			"Your answers surprise with fresh, unconventional viewpoints.",
	},
	{
		ID:          "scholar",
		Name:        "Scholar",
		Description: "Academic and precise",
		Prompt: "Your personality: scholarly and meticulous. " +
			"Write with intellectual rigor and factual precision. " +
			"Your answers are well-structured, informed, and logically sound.",
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
			Text: "When you solve a problem, what matters most?",
			Options: []Option{
				{Key: "A", Text: "Getting to the point with zero fluff", Weights: map[string]int{"minimalist": 3, "scholar": 1}},
				{Key: "B", Text: "Finding a creative angle no one expected", Weights: map[string]int{"rebel": 3, "witty": 1}},
				{Key: "C", Text: "Building a solution that feels right", Weights: map[string]int{"warm": 2, "wise": 2}},
				{Key: "D", Text: "Delivering it with precision and style", Weights: map[string]int{"proud": 3, "poetic": 1}},
			},
		},
		{
			Text: "How would others describe the way you communicate?",
			Options: []Option{
				{Key: "A", Text: "Clear and thorough — every detail accounted for", Weights: map[string]int{"scholar": 3, "proud": 1}},
				{Key: "B", Text: "Warm and genuine — people feel at ease", Weights: map[string]int{"warm": 3, "wise": 1}},
				{Key: "C", Text: "Sharp and clever — often with a twist", Weights: map[string]int{"witty": 3, "rebel": 1}},
				{Key: "D", Text: "Vivid and expressive — words paint pictures", Weights: map[string]int{"poetic": 3, "wise": 1}},
			},
		},
		{
			Text: "Pick the motto that resonates most.",
			Options: []Option{
				{Key: "A", Text: `"Less is more."`, Weights: map[string]int{"minimalist": 3, "proud": 1}},
				{Key: "B", Text: `"Question everything."`, Weights: map[string]int{"rebel": 3, "scholar": 1}},
				{Key: "C", Text: `"Make them smile while they learn."`, Weights: map[string]int{"witty": 3, "warm": 1}},
				{Key: "D", Text: `"Seek the deeper meaning."`, Weights: map[string]int{"wise": 3, "poetic": 1}},
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
	return fmt.Sprintf(`You are writing a personality profile for an AI agent on a work platform.

Base personality template:
%s

The agent's owner chose these traits during setup:
- Problem-solving style: %s
- Communication style: %s
- Personal motto: %s

Write a 2-3 sentence personality description for this agent. It should be written in second person ("Your personality:..." or "You are..."). Incorporate the specific traits from the chosen answers to make this personality feel unique, not generic. Stay true to the base template's core character but add distinctive flavor from the answers.

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
