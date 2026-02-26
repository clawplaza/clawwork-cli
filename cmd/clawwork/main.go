package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/clawplaza/clawwork-cli/internal/api"
	"github.com/clawplaza/clawwork-cli/internal/config"
	"github.com/clawplaza/clawwork-cli/internal/daemon"
	"github.com/clawplaza/clawwork-cli/internal/knowledge"
	"github.com/clawplaza/clawwork-cli/internal/llm"
	"github.com/clawplaza/clawwork-cli/internal/miner"
	"github.com/clawplaza/clawwork-cli/internal/updater"
	"github.com/clawplaza/clawwork-cli/internal/web"
)

// Set at build time via ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	api.SetVersion(version)

	root := &cobra.Command{
		Use:   "clawwork",
		Short: "ClawWork — AI labor market CLI",
		Long:  "ClawWork CLI — Official client for the ClawWork AI Agent labor market.",
	}

	root.AddCommand(initCmd(), inscCmd(), statusCmd(), configCmd(), soulCmd(), specCmd(), versionCmd(), updateCmd(),
		installCmd(), uninstallCmd(), startCmd(), stopCmd(), restartCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// ── init command ──

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize config and register agent",
		RunE:  runInit,
	}
}

func runInit(_ *cobra.Command, _ []string) error {
	fmt.Printf("Welcome to ClawWork!  (v%s)\n", version)

	// Non-blocking remote version check
	type versionResult struct {
		info *updater.VersionInfo
		err  error
	}
	versionCh := make(chan versionResult, 1)
	go func() {
		info, err := updater.CheckUpdate(version)
		versionCh <- versionResult{info, err}
	}()

	// Print update hint if result arrives quickly
	select {
	case r := <-versionCh:
		if r.err == nil && r.info != nil {
			fmt.Printf("Update available: v%s → v%s  (run: clawwork update)\n", version, r.info.Version)
		}
	case <-time.After(2 * time.Second):
		// Don't block init flow
	}
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)

	// Check if config already exists
	if _, err := os.Stat(config.Path()); err == nil {
		fmt.Printf("Config already exists at %s\n", config.Path())
		fmt.Print("Overwrite? [y/N]: ")
		scanner.Scan()
		if strings.ToLower(strings.TrimSpace(scanner.Text())) != "y" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	// Choose mode
	fmt.Println("Setup mode:")
	fmt.Println("  1. Existing agent — I already have an API key")
	fmt.Println("  2. New agent      — register a new agent on the platform")
	fmt.Print("Choose [1]: ")
	scanner.Scan()
	mode := strings.TrimSpace(scanner.Text())
	if mode == "" {
		mode = "1"
	}
	fmt.Println()

	switch mode {
	case "1":
		return runInitExisting(scanner)
	case "2":
		return runInitNew(scanner)
	default:
		return fmt.Errorf("invalid choice: %s", mode)
	}
}

func runInitNew(scanner *bufio.Scanner) error {
	cfg := config.DefaultConfig()

	// Agent name
	fmt.Print("Agent name (1-30, alphanumeric + underscore): ")
	scanner.Scan()
	cfg.Agent.Name = strings.TrimSpace(scanner.Text())
	if cfg.Agent.Name == "" {
		return fmt.Errorf("agent name is required")
	}

	// Token ID
	fmt.Print("Token ID to inscribe (25-1024): ")
	scanner.Scan()
	tokenStr := strings.TrimSpace(scanner.Text())
	if tokenStr != "" {
		tid, err := strconv.Atoi(tokenStr)
		if err != nil || tid < 25 || tid > 1024 {
			return fmt.Errorf("invalid token ID: must be 25-1024")
		}
		cfg.Agent.TokenID = tid
	}

	// LLM configuration
	if err := collectLLMConfig(scanner, cfg); err != nil {
		return err
	}

	// Register agent
	fmt.Print("\nRegistering agent... ")
	client := api.New("")
	resp, err := client.Register(context.Background(), cfg.Agent.Name, cfg.Agent.TokenID)
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}

	if resp.Error == "ALREADY_REGISTERED" || resp.Error == "NAME_TAKEN" {
		fmt.Println("agent name already taken.")
		fmt.Print("Enter your existing API key: ")
		scanner.Scan()
		cfg.Agent.APIKey = strings.TrimSpace(scanner.Text())
		if cfg.Agent.APIKey == "" {
			return fmt.Errorf("API key is required for existing agents")
		}
	} else if resp.APIKey != "" {
		cfg.Agent.APIKey = resp.APIKey
		fmt.Println("done!")
		fmt.Printf("Agent ID: %s\n", resp.AgentID)
	} else if resp.Error != "" {
		return fmt.Errorf("registration error: %s — %s", resp.Error, resp.Message)
	}

	// Save config
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("\nConfig saved to %s\n", config.Path())

	// Offer personality setup.
	needSoul := !knowledge.SoulExists()
	if !needSoul {
		if _, soulErr := knowledge.LoadSoul(cfg.Agent.APIKey); soulErr != nil {
			needSoul = true
			fmt.Println("\nExisting soul cannot be decrypted with current API key.")
		}
	}
	if needSoul {
		fmt.Print("\nSet up agent Soul? [Y/n]: ")
		scanner.Scan()
		soulAnswer := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if soulAnswer == "" || soulAnswer == "y" || soulAnswer == "yes" {
			fmt.Println()
			if err := generateSoul(scanner, cfg.Agent.APIKey); err != nil {
				fmt.Printf("Warning: soul generation failed: %s\n", err)
			}
		}
	}

	if resp.MiningReady {
		fmt.Print("\nStart inscribing now? [Y/n]: ")
		scanner.Scan()
		answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if answer == "" || answer == "y" || answer == "yes" {
			fmt.Println()
			return runInsc(nil, nil)
		}
		fmt.Println("\nRun 'clawwork insc' to begin when ready.")
	} else {
		fmt.Println("\nNext steps:")
		fmt.Println("  1. Your owner must claim this agent at https://work.clawplaza.ai/my-agent")
		fmt.Println("  2. Your owner must bind a wallet address")
		fmt.Println("  3. Then run: clawwork insc")
	}

	return nil
}

func runInitExisting(scanner *bufio.Scanner) error {
	cfg := config.DefaultConfig()

	// Agent API key (from platform registration, not LLM key)
	fmt.Print("ClawWork agent API key (from registration or My Agent page): ")
	scanner.Scan()
	cfg.Agent.APIKey = strings.TrimSpace(scanner.Text())
	if cfg.Agent.APIKey == "" {
		return fmt.Errorf("agent API key is required")
	}

	// Verify API key by fetching status
	fmt.Print("Verifying... ")
	client := api.New(cfg.Agent.APIKey)
	status, err := client.Status(context.Background())
	if err != nil {
		fmt.Println("failed!")
		return fmt.Errorf("could not verify API key: %w", err)
	}
	if status.Agent.ID == "" {
		fmt.Println("failed!")
		return fmt.Errorf("invalid API key")
	}
	fmt.Printf("ok! Agent: %s\n\n", status.Agent.ID)

	// Token ID
	fmt.Print("Token ID to inscribe (25-1024): ")
	scanner.Scan()
	tokenStr := strings.TrimSpace(scanner.Text())
	if tokenStr != "" {
		tid, err := strconv.Atoi(tokenStr)
		if err != nil || tid < 25 || tid > 1024 {
			return fmt.Errorf("invalid token ID: must be 25-1024")
		}
		cfg.Agent.TokenID = tid
	}

	// LLM configuration
	if err := collectLLMConfig(scanner, cfg); err != nil {
		return err
	}

	// Save config
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("\nConfig saved to %s\n", config.Path())

	// Offer personality setup.
	needSoul := !knowledge.SoulExists()
	if !needSoul {
		if _, soulErr := knowledge.LoadSoul(cfg.Agent.APIKey); soulErr != nil {
			needSoul = true
			fmt.Println("\nExisting soul cannot be decrypted with current API key.")
		}
	}
	if needSoul {
		fmt.Print("\nSet up agent Soul? [Y/n]: ")
		scanner.Scan()
		soulAnswer := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if soulAnswer == "" || soulAnswer == "y" || soulAnswer == "yes" {
			fmt.Println()
			if err := generateSoul(scanner, cfg.Agent.APIKey); err != nil {
				fmt.Printf("Warning: soul generation failed: %s\n", err)
			}
		}
	}

	fmt.Print("\nStart inscribing now? [Y/n]: ")
	scanner.Scan()
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if answer == "" || answer == "y" || answer == "yes" {
		fmt.Println()
		return runInsc(nil, nil)
	}

	fmt.Println("\nRun 'clawwork insc' to begin when ready.")
	return nil
}

// collectLLMConfig prompts the user for LLM provider settings.
// Default is Kimi (free tier available, no credit card required).
func collectLLMConfig(scanner *bufio.Scanner, cfg *config.Config) error {
	fmt.Println()
	fmt.Println("LLM provider (for answering challenges):")
	fmt.Println("  1. Kimi    (kimi-k2.5)         — recommended, free tier available")
	fmt.Println("  2. OpenAI  (gpt-4o-mini)")
	fmt.Println("  3. Anthropic (claude-haiku)")
	fmt.Println("  4. Ollama  (local, free)        — requires ollama installed")
	fmt.Println("  5. Custom OpenAI-compatible")
	fmt.Println("  6. Platform                     — requires platform key (plat_xxx)")
	fmt.Print("Choose [1]: ")
	scanner.Scan()
	providerChoice := strings.TrimSpace(scanner.Text())
	if providerChoice == "" {
		providerChoice = "1"
	}

	// Each provider has a key URL shown after selection.
	var keyURL string

	switch providerChoice {
	case "1": // Kimi
		cfg.LLM.Provider = "openai"
		cfg.LLM.BaseURL = "https://api.moonshot.cn/v1"
		cfg.LLM.Model = "kimi-k2.5"
		keyURL = "https://platform.moonshot.cn/console/api-keys"
	case "2": // OpenAI
		cfg.LLM.Provider = "openai"
		cfg.LLM.BaseURL = "https://api.openai.com/v1"
		cfg.LLM.Model = "gpt-4o-mini"
		keyURL = "https://platform.openai.com/api-keys"
	case "3": // Anthropic
		cfg.LLM.Provider = "anthropic"
		cfg.LLM.Model = "claude-haiku-4-5-20251001"
		keyURL = "https://console.anthropic.com/settings/keys"
	case "4": // Ollama
		cfg.LLM.Provider = "ollama"
		cfg.LLM.BaseURL = "http://localhost:11434"
		cfg.LLM.Model = "llama3.2"
		fmt.Printf("Ollama model (default: %s): ", cfg.LLM.Model)
		scanner.Scan()
		if m := strings.TrimSpace(scanner.Text()); m != "" {
			cfg.LLM.Model = m
		}
		return nil // no API key needed
	case "5": // Custom
		cfg.LLM.Provider = "openai"
		fmt.Print("API base URL: ")
		scanner.Scan()
		cfg.LLM.BaseURL = strings.TrimSpace(scanner.Text())
		if cfg.LLM.BaseURL == "" {
			return fmt.Errorf("API base URL is required")
		}
		fmt.Print("Model name: ")
		scanner.Scan()
		cfg.LLM.Model = strings.TrimSpace(scanner.Text())
		if cfg.LLM.Model == "" {
			return fmt.Errorf("model name is required")
		}
		keyURL = ""
	case "6": // Platform
		cfg.LLM.Provider = "platform"
		fmt.Print("Platform key (plat_xxx): ")
		scanner.Scan()
		cfg.LLM.APIKey = strings.TrimSpace(scanner.Text())
		if cfg.LLM.APIKey == "" {
			return fmt.Errorf("platform key is required")
		}
		return nil
	default:
		return fmt.Errorf("invalid choice: %s", providerChoice)
	}

	// Show where to get an API key
	if keyURL != "" {
		fmt.Println()
		fmt.Printf("  Get your API key here: %s\n", keyURL)
		fmt.Println()
	}

	fmt.Print("API key: ")
	scanner.Scan()
	cfg.LLM.APIKey = strings.TrimSpace(scanner.Text())
	if cfg.LLM.APIKey == "" {
		return fmt.Errorf("API key is required")
	}

	return nil
}

// ── insc command ──

func inscCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "insc",
		Short: "Start inscription challenges",
		RunE:  runInsc,
	}
	cmd.Flags().IntP("token-id", "t", 0, "Override target token ID")
	cmd.Flags().BoolP("verbose", "v", false, "Verbose output")
	cmd.Flags().Bool("no-web", false, "Disable web console")
	cmd.Flags().IntP("port", "p", 0, "Web console port (default: auto from 2526)")
	return cmd
}

func runInsc(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	// Setup logger
	logLevel := cfg.Logging.Level
	if cmd != nil {
		if verbose, _ := cmd.Flags().GetBool("verbose"); verbose {
			logLevel = "debug"
		}
	}
	miner.SetupLogger(logLevel)

	// Token ID override
	tokenID := cfg.Agent.TokenID
	if cmd != nil {
		if tid, _ := cmd.Flags().GetInt("token-id"); tid > 0 {
			if tid < 25 || tid > 1024 {
				return fmt.Errorf("token-id must be between 25 and 1024")
			}
			tokenID = tid
		}
	}

	// Load platform knowledge
	kn, err := knowledge.Load(cfg.Agent.APIKey)
	if err != nil {
		return err
	}

	// Create LLM provider with enhanced system prompt.
	// 2048 tokens: thinking models (Kimi K2.5, DeepSeek-R1) need room for
	// internal reasoning + the actual short answer in the content field.
	llmProvider, err := llm.NewProvider(&cfg.LLM, kn.SystemPrompt(), 2048)
	if err != nil {
		return err
	}

	// Create API client
	apiClient := api.New(cfg.Agent.APIKey)

	// Load state
	state := miner.LoadState()

	// Create miner
	m := &miner.Miner{
		API:       apiClient,
		LLM:       llmProvider,
		State:     state,
		TokenID:   tokenID,
		Knowledge: kn,
	}
	m.SetVersion(version)

	// Start web console (unless --no-web)
	noWeb := false
	webPort := 0
	webPortPinned := false
	if cmd != nil {
		noWeb, _ = cmd.Flags().GetBool("no-web")
		if p, _ := cmd.Flags().GetInt("port"); p > 0 {
			webPort = p
			webPortPinned = true
		}
	}
	if !noWeb {
		chatPrompt := web.ChatSystemPrompt(kn.Soul)
		chatProvider, chatErr := llm.NewProvider(&cfg.LLM, chatPrompt, 1024)
		if chatErr != nil {
			fmt.Printf("Warning: chat provider failed: %s (web console chat disabled)\n", chatErr)
		} else {
			// Fetch agent info from platform for the console header.
			agentInfo := web.AgentInfo{Name: cfg.Agent.Name}
			if status, err := apiClient.Status(context.Background()); err == nil {
				if status.Agent.Name != "" {
					agentInfo.Name = status.Agent.Name
				}
				agentInfo.AvatarURL = status.Agent.AvatarURL
			}
			srv, hub, ctrl := web.New(chatProvider, state, tokenID, agentInfo, apiClient, webPort)
			actualPort, startErr := srv.Start(webPortPinned)
			if startErr != nil {
				fmt.Printf("Warning: web console unavailable: %s\n", startErr)
			} else {
				m.OnEvent = func(eventType, message string, data any) {
					hub.Publish(web.Event{Type: eventType, Message: message, Data: data})
				}
				m.Ctrl = ctrl
				defer func() {
					shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer shutdownCancel()
					srv.Shutdown(shutdownCtx)
				}()
				fmt.Printf("Console: http://127.0.0.1:%d\n", actualPort)
			}
		}
	}

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nShutting down gracefully... waiting for current operation to finish.")
		cancel()
	}()

	fmt.Printf("ClawWork %s — inscribing token #%d\n", version, tokenID)
	fmt.Printf("LLM: %s\n", llmProvider.Name())
	if kn.HasSoul() {
		fmt.Printf("Soul: active\n")
	}
	fmt.Println()

	return m.Run(ctx)
}

// ── status command ──

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check agent status",
		RunE:  runStatus,
	}
}

func runStatus(_ *cobra.Command, _ []string) error {
	// Show service status if platform supports it.
	if mgr, err := daemon.New(); err == nil {
		st, _ := mgr.Status()
		if st != nil {
			switch {
			case !st.Installed:
				fmt.Println("Service:      not installed")
			case st.Running:
				fmt.Printf("Service:      running (PID %d)\n", st.PID)
			default:
				fmt.Println("Service:      stopped")
			}
			fmt.Printf("Log file:     %s\n", st.LogPath)
			fmt.Println()
		}
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	client := api.New(cfg.Agent.APIKey)
	resp, err := client.Status(context.Background())
	if err != nil {
		return fmt.Errorf("failed to fetch status: %w", err)
	}

	fmt.Printf("Agent:        %s (%s)\n", resp.Agent.Name, resp.Agent.ID)
	fmt.Printf("Wallet:       %s\n", resp.Agent.WalletAddress)
	fmt.Printf("Inscriptions: %d total, %d confirmed\n", resp.Inscriptions.Total, resp.Inscriptions.Confirmed)
	fmt.Printf("CW Earned:    %d\n", resp.Inscriptions.TotalCW)
	fmt.Printf("NFT Hit:      %v\n", resp.Inscriptions.Hit)
	fmt.Printf("Platform:     %s (%d NFTs remaining)\n", resp.Activity.Status, resp.Activity.NFTsRemaining)
	if resp.GenesisNFT != nil {
		fmt.Printf("Genesis NFT:  #%d\n", resp.GenesisNFT.TokenID)
	}

	// Also show local state
	state := miner.LoadState()
	if state.TotalInscriptions > 0 {
		fmt.Printf("\n--- Local Stats ---\n")
		fmt.Printf("Session inscriptions: %d\n", state.TotalInscriptions)
		fmt.Printf("Session CW earned:    %d\n", state.TotalCWEarned)
		fmt.Printf("Session NFT hits:     %d\n", state.TotalHits)
	}

	return nil
}

// ── config command ──

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage configuration",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "show",
			Short: "Show current config (API keys redacted)",
			RunE:  runConfigShow,
		},
		&cobra.Command{
			Use:   "path",
			Short: "Print config file path",
			Run: func(_ *cobra.Command, _ []string) {
				fmt.Println(config.Path())
			},
		},
	)
	return cmd
}

func runConfigShow(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	redacted := cfg.Redact()
	return toml.NewEncoder(os.Stdout).Encode(redacted)
}

// ── version command ──

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Printf("clawwork %s (commit: %s, built: %s)\n", version, commit, date)
		},
	}
}

// ── update command ──

func updateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update clawwork to the latest version",
		RunE:  runUpdate,
	}
	cmd.Flags().Bool("check", false, "Only check for updates, don't install")
	return cmd
}

func runUpdate(cmd *cobra.Command, _ []string) error {
	checkOnly, _ := cmd.Flags().GetBool("check")

	fmt.Printf("Current version: %s\n", version)
	fmt.Print("Checking for updates... ")

	info, err := updater.CheckUpdate(version)
	if err != nil {
		return err
	}
	if info == nil {
		fmt.Println("already up to date.")
		return nil
	}

	fmt.Printf("v%s available!\n", info.Version)
	if info.Changelog != "" {
		fmt.Printf("Changelog: %s\n", info.Changelog)
	}

	if checkOnly {
		return nil
	}

	fmt.Println()
	return updater.Apply(info)
}

// ── soul command ──

func soulCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "soul",
		Short: "Generate or manage agent personality",
		RunE:  runSoulGenerate,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "generate",
			Short: "Interactive personality quiz + LLM generation",
			RunE:  runSoulGenerate,
		},
		&cobra.Command{
			Use:   "show",
			Short: "Show current soul content",
			RunE:  runSoulShow,
		},
		&cobra.Command{
			Use:   "reset",
			Short: "Remove custom soul, revert to default",
			RunE: func(_ *cobra.Command, _ []string) error {
				if err := knowledge.ResetSoul(); err != nil {
					return err
				}
				fmt.Println("Soul reset. Using default personality.")
				return nil
			},
		},
	)
	return cmd
}

func runSoulGenerate(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config required — run 'clawwork init' first: %w", err)
	}

	scanner := bufio.NewScanner(os.Stdin)

	if knowledge.SoulExists() {
		// Try decrypting with current key.
		if _, err := knowledge.LoadSoul(cfg.Agent.APIKey); err == nil {
			// Valid soul with current key — immutable.
			fmt.Println("Soul already exists and cannot be modified once generated.")
			fmt.Println("To start over: clawwork soul reset")
			return nil
		}
		// Key changed or file corrupted — allow overwrite.
		fmt.Println("Existing soul cannot be decrypted (API key may have changed).")
		fmt.Print("Generate a new soul? [y/N]: ")
		scanner.Scan()
		if strings.ToLower(strings.TrimSpace(scanner.Text())) != "y" {
			fmt.Println("Aborted.")
			return nil
		}
		fmt.Println()
	}

	return generateSoul(scanner, cfg.Agent.APIKey)
}

// generateSoul runs the personality quiz + LLM generation flow.
// Extracted so it can be called from both `soul generate` and `init`.
// The apiKey is used to encrypt the soul file with AES-256-GCM.
func generateSoul(scanner *bufio.Scanner, apiKey string) error {
	fmt.Println("Let's discover your agent's personality.")
	fmt.Println()

	questions := knowledge.Questions()
	answerIndices := make([]int, len(questions))
	answerTexts := make([]string, len(questions))

	for i, q := range questions {
		fmt.Printf("Q%d. %s\n", i+1, q.Text)
		for _, opt := range q.Options {
			fmt.Printf("  %s. %s\n", opt.Key, opt.Text)
		}
		fmt.Print("Choose [A/B/C/D]: ")
		scanner.Scan()
		idx := letterToIndex(strings.TrimSpace(scanner.Text()))
		answerIndices[i] = idx
		answerTexts[i] = q.Options[idx].Text
		fmt.Println()
	}

	// Score answers to select base template
	preset := knowledge.ScoreAnswers(answerIndices)

	// Try LLM personalization
	var soulText string
	cfg, cfgErr := config.Load()
	if cfgErr != nil {
		fmt.Println("LLM not configured. Using base template.")
		soulText = preset.Prompt
	} else {
		provider, llmErr := llm.NewProvider(&cfg.LLM, knowledge.GenerationSystemPrompt(), 256)
		if llmErr != nil {
			fmt.Printf("LLM setup failed: %s. Using base template.\n", llmErr)
			soulText = preset.Prompt
		} else {
			fmt.Print("Generating personality... ")
			prompt := knowledge.GeneratePrompt(preset, answerTexts)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			result, genErr := provider.Answer(ctx, prompt)
			if genErr != nil {
				fmt.Printf("failed: %s\nUsing base template.\n", genErr)
				soulText = preset.Prompt
			} else if cleaned, ok := knowledge.ValidateGenerated(result); ok {
				soulText = cleaned
				fmt.Println("done!")
			} else {
				fmt.Println("unexpected output. Using base template.")
				soulText = preset.Prompt
			}
		}
	}

	// Save and display
	if err := knowledge.SaveSoul(apiKey, soulText); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Your agent's soul:")
	fmt.Println()
	fmt.Printf("  %s\n", soulText)
	fmt.Println()
	fmt.Printf("Saved to %s (encrypted)\n", knowledge.SoulPath())
	fmt.Println("Soul is sealed and cannot be modified once generated.")
	return nil
}

// letterToIndex converts A/B/C/D (or 1/2/3/4) to 0-3. Defaults to 0.
func letterToIndex(s string) int {
	switch strings.ToUpper(s) {
	case "A", "1":
		return 0
	case "B", "2":
		return 1
	case "C", "3":
		return 2
	case "D", "4":
		return 3
	default:
		return 0
	}
}

func runSoulShow(_ *cobra.Command, _ []string) error {
	if !knowledge.SoulExists() {
		fmt.Println("No soul configured.")
		fmt.Println("Run 'clawwork soul generate' to create one.")
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config required: %w", err)
	}

	soul, err := knowledge.LoadSoul(cfg.Agent.APIKey)
	if err != nil {
		return fmt.Errorf("failed to read soul: %w", err)
	}

	fmt.Println("Current soul:")
	fmt.Println()
	fmt.Println(soul)
	fmt.Println()
	fmt.Printf("File: %s (encrypted)\n", knowledge.SoulPath())
	return nil
}

// ── spec command ──

func specCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "spec",
		Short: "Show built-in platform knowledge",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			kn, err := knowledge.Load(cfg.Agent.APIKey)
			if err != nil {
				return err
			}

			fmt.Println("--- Base ---")
			fmt.Println(kn.Base)
			fmt.Println()

			fmt.Println("--- Soul ---")
			if kn.HasSoul() {
				fmt.Println(kn.Soul)
			} else {
				fmt.Println("(No soul configured)")
			}
			fmt.Println()

			fmt.Println("--- Challenges ---")
			fmt.Println(kn.Challenges)
			fmt.Println()

			fmt.Println("--- Platform ---")
			fmt.Println(kn.Platform)
			fmt.Println()

			fmt.Println("--- APIs ---")
			fmt.Println(kn.APIs)

			return nil
		},
	}
}

// ── service management commands ──

func installCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install ClawWork as a background service",
		RunE:  runInstall,
	}
}

func uninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove background service",
		RunE:  runUninstall,
	}
}

func startCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the background service",
		RunE:  runStart,
	}
}

func stopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the background service",
		RunE:  runStop,
	}
}

func restartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the background service",
		RunE:  runRestart,
	}
}

func runInstall(_ *cobra.Command, _ []string) error {
	// Config must exist before installing.
	if _, err := config.Load(); err != nil {
		return fmt.Errorf("config not found — run 'clawwork init' first")
	}

	mgr, err := daemon.New()
	if err != nil {
		return err
	}

	// Check if already installed.
	st, _ := mgr.Status()
	if st != nil && st.Installed {
		fmt.Println("Service is already installed. Reinstalling...")
		_ = mgr.Uninstall()
	}

	fmt.Println("Installing ClawWork as background service...")
	if err := mgr.Install(); err != nil {
		return fmt.Errorf("install failed: %w", err)
	}

	fmt.Printf("Log file:  %s\n", daemon.LogPath())
	fmt.Println("Service installed and started.")
	return nil
}

func runUninstall(_ *cobra.Command, _ []string) error {
	mgr, err := daemon.New()
	if err != nil {
		return err
	}

	st, _ := mgr.Status()
	if st != nil && !st.Installed {
		fmt.Println("Service not installed.")
		return nil
	}

	if err := mgr.Uninstall(); err != nil {
		return fmt.Errorf("uninstall failed: %w", err)
	}
	fmt.Println("Service stopped and removed.")
	return nil
}

func runStart(_ *cobra.Command, _ []string) error {
	mgr, err := daemon.New()
	if err != nil {
		return err
	}

	st, _ := mgr.Status()
	if st != nil && !st.Installed {
		return fmt.Errorf("service not installed — run 'clawwork install' first")
	}

	if err := mgr.Start(); err != nil {
		return fmt.Errorf("start failed: %w", err)
	}
	fmt.Println("Service started.")
	return nil
}

func runStop(_ *cobra.Command, _ []string) error {
	mgr, err := daemon.New()
	if err != nil {
		return err
	}

	if err := mgr.Stop(); err != nil {
		return fmt.Errorf("stop failed: %w", err)
	}
	fmt.Println("Service stopped.")
	return nil
}

func runRestart(_ *cobra.Command, _ []string) error {
	mgr, err := daemon.New()
	if err != nil {
		return err
	}

	st, _ := mgr.Status()
	if st != nil && !st.Installed {
		return fmt.Errorf("service not installed — run 'clawwork install' first")
	}

	if err := mgr.Restart(); err != nil {
		return fmt.Errorf("restart failed: %w", err)
	}
	fmt.Println("Service restarted.")
	return nil
}
