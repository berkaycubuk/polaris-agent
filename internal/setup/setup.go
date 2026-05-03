// Package setup provides an interactive first-run wizard for Polaris Agent.
// It creates .env with an auth token and LLM configuration.
package setup

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Providers holds the well-known LLM provider presets.
var Providers = []struct {
	Name    string
	BaseURL string
	Models  []string
}{
	{
		Name:    "OpenAI",
		BaseURL: "https://api.openai.com/v1",
		Models:  []string{"gpt-4o-mini", "gpt-4o", "gpt-4.1-mini", "gpt-4.1", "o4-mini"},
	},
	{
		Name:    "Google Gemini",
		BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai",
		Models:  []string{"gemini-2.5-flash", "gemini-2.5-flash-lite", "gemini-2.5-pro"},
	},
	{
		Name:    "OpenRouter",
		BaseURL: "https://openrouter.ai/api/v1",
		Models:  []string{"openai/gpt-4o-mini", "anthropic/claude-sonnet-4-20250514", "google/gemini-2.5-flash"},
	},
	{
		Name:    "Groq",
		BaseURL: "https://api.groq.com/openai/v1",
		Models:  []string{"llama-3.3-70b-versatile", "llama-3.1-8b-instant"},
	},
	{
		Name:    "Z.AI",
		BaseURL: "https://api.z.ai/api/paas/v4",
		Models:  []string{"glm-4.6", "glm-4.5-air", "glm-4.5", "glm-5-turbo"},
	},
	{
		Name:    "Ollama (local)",
		BaseURL: "http://localhost:11434/v1",
		Models:  []string{"llama3", "mistral", "qwen2"},
	},
	{
		Name: "Custom endpoint",
	},
}

// Run executes the interactive setup wizard.
// envPath is the target .env file path (e.g. ".env").
// It writes the configuration file — the server handles data directory
// seeding on first startup.
func Run(envPath string) error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println()
	fmt.Println("  ╔═══════════════════════════════════════════╗")
	fmt.Println("  ║         ★ Polaris Agent Setup ★           ║")
	fmt.Println("  ╚═══════════════════════════════════════════╝")
	fmt.Println()
	fmt.Println("  Let's get your personal AI companion configured.")
	fmt.Println()

	// Check if .env already exists
	existingVars := map[string]string{}
	if data, err := os.ReadFile(envPath); err == nil {
		fmt.Printf("  Found existing config at %s — we'll update it.\n\n", envPath)
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if eq := strings.IndexByte(line, '='); eq > 0 {
				k := strings.TrimSpace(line[:eq])
				v := strings.TrimSpace(line[eq+1:])
				v = strings.Trim(v, `"'`)
				existingVars[k] = v
			}
		}
	}

	// Step 1: LLM Provider
	fmt.Println("  ── Step 1: LLM Provider ──────────────────────")
	fmt.Println()
	for i, p := range Providers {
		fmt.Printf("    %d) %s\n", i+1, p.Name)
	}
	fmt.Println()

	providerIdx := promptInt(reader, "  Choose your LLM provider", 1, len(Providers))
	provider := Providers[providerIdx-1]

	var baseURL, model, apiKey string

	if provider.Name == "Custom endpoint" {
		baseURL = promptString(reader, "  Base URL (OpenAI-compatible)", existingVars["LLM_BASE_URL"])
		fmt.Println()
		model = promptString(reader, "  Model name", existingVars["LLM_MODEL"])
	} else {
		baseURL = provider.BaseURL
		fmt.Printf("  → Using %s\n", provider.Name)
		fmt.Println()

		if len(provider.Models) > 0 {
			fmt.Println("  Popular models:")
			for i, m := range provider.Models {
				fmt.Printf("    %d) %s\n", i+1, m)
			}
			fmt.Println("    0) other (type a model name)")
			fmt.Println()
			choice := promptInt(reader, "  Choose a model", 0, len(provider.Models))
			if choice == 0 {
				model = promptString(reader, "  Model name", existingVars["LLM_MODEL"])
			} else {
				model = provider.Models[choice-1]
			}
		} else {
			model = promptString(reader, "  Model name", existingVars["LLM_MODEL"])
		}
	}

	fmt.Println()

	// Step 2: API Key
	fmt.Println("  ── Step 2: API Key ───────────────────────────")
	fmt.Println()
	if provider.Name == "Ollama (local)" {
		fmt.Println("  Ollama doesn't need an API key. Using placeholder.")
		apiKey = "ollama"
	} else {
		existing := existingVars["LLM_API_KEY"]
		if existing != "" {
			existing = maskKey(existing)
		}
		apiKey = promptSecret(reader, "  Enter your API key", existing)
	}

	fmt.Println()

	// Step 3: Auth Token
	fmt.Println("  ── Step 3: Authentication ────────────────────")
	fmt.Println()
	existingToken := existingVars["AUTH_TOKEN"]
	if existingToken == "change-me" || existingToken == "" {
		token, err := generateToken()
		if err != nil {
			return fmt.Errorf("generate token: %w", err)
		}
		fmt.Printf("  Generated auth token: %s\n", token)
		fmt.Println("  (Used to authenticate API requests. Save this — it won't be shown again.)")
		existingToken = token
	} else {
		fmt.Printf("  Existing auth token: %s (keeping)\n", maskKey(existingToken))
	}

	fmt.Println()

	// Step 4: Telegram (optional)
	fmt.Println("  ── Step 4: Telegram (optional) ───────────────")
	fmt.Println()
	telegramToken := existingVars["TELEGRAM_BOT_TOKEN"]
	if promptYesNo(reader, "  Do you want to set up Telegram?", telegramToken != "") {
		telegramToken = promptString(reader, "  Bot token (from @BotFather)", telegramToken)
	}

	fmt.Println()

	// Step 5: Image understanding (optional)
	fmt.Println("  ── Step 5: Image understanding (optional) ────")
	fmt.Println()
	var captionBaseURL, captionModel, captionAPIKey string
	if promptYesNo(reader, "  Enable image understanding?", existingVars["IMAGE_CAPTION_BASE_URL"] != "") {
		fmt.Println()
		fmt.Println("  A separate vision model captions image attachments.")
		fmt.Println("  Google Gemini Flash works great and is cheap/free.")
		fmt.Println()
		captionBaseURL = promptString(reader, "  Vision model base URL",
			defaultStr(existingVars["IMAGE_CAPTION_BASE_URL"], "https://generativelanguage.googleapis.com/v1beta/openai"))
		captionModel = promptString(reader, "  Vision model name",
			defaultStr(existingVars["IMAGE_CAPTION_MODEL"], "gemini-2.5-flash-lite"))
		captionAPIKey = promptString(reader, "  Vision model API key", maskKey(existingVars["IMAGE_CAPTION_API_KEY"]))
	}

	fmt.Println()

	// Build .env
	if err := os.MkdirAll(filepath.Dir(envPath), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	var env strings.Builder
	env.WriteString("# --- Main LLM (the agent's brain) ---\n")
	fmt.Fprintf(&env, "LLM_BASE_URL=%s\n", baseURL)
	fmt.Fprintf(&env, "LLM_MODEL=%s\n", model)
	fmt.Fprintf(&env, "LLM_API_KEY=%s\n", apiKey)
	env.WriteString("\n")
	env.WriteString("# --- Auth ---\n")
	fmt.Fprintf(&env, "AUTH_TOKEN=%s\n", existingToken)

	if telegramToken != "" {
		env.WriteString("\n# --- Telegram ---\n")
		fmt.Fprintf(&env, "TELEGRAM_BOT_TOKEN=%s\n", telegramToken)
	}

	if captionBaseURL != "" {
		env.WriteString("\n# --- Image captioner ---\n")
		fmt.Fprintf(&env, "IMAGE_CAPTION_BASE_URL=%s\n", captionBaseURL)
		fmt.Fprintf(&env, "IMAGE_CAPTION_MODEL=%s\n", captionModel)
		fmt.Fprintf(&env, "IMAGE_CAPTION_API_KEY=%s\n", captionAPIKey)
	}

	env.WriteString("\n")

	if err := os.WriteFile(envPath, []byte(env.String()), 0o600); err != nil {
		return fmt.Errorf("write .env: %w", err)
	}

	// Summary
	fmt.Println("  ╔═══════════════════════════════════════════╗")
	fmt.Println("  ║          ✓ Setup complete!                 ║")
	fmt.Println("  ╚═══════════════════════════════════════════╝")
	fmt.Println()
	fmt.Printf("  Config:  %s\n", envPath)
	fmt.Println()
	fmt.Println("  Start your agent:")
	fmt.Println()
	fmt.Println("    docker compose up -d")
	fmt.Println()
	fmt.Println("  Test with curl:")
	fmt.Println()
	fmt.Printf("    curl -s localhost:8080/chat \\\n")
	fmt.Printf("      -H \"Authorization: Bearer %s\" \\\n", existingToken)
	fmt.Printf("      -H \"content-type: application/json\" \\\n")
	fmt.Printf("      -d '{\"session\":\"me\",\"message\":\"hello\"}'\n")
	fmt.Println()
	if telegramToken != "" {
		fmt.Println("  Telegram bot is configured — message your bot on Telegram!")
		fmt.Println()
	}
	fmt.Println("  Diagnose issues anytime:")
	fmt.Println()
	fmt.Println("    polaris doctor")
	fmt.Println()

	return nil
}

// --- Prompting helpers ---

func promptString(reader *bufio.Reader, p string, fallback string) string {
	if fallback != "" {
		fmt.Printf("%s [%s]: ", p, fallback)
	} else {
		fmt.Printf("%s: ", p)
	}
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return fallback
	}
	return line
}

func promptSecret(reader *bufio.Reader, p string, fallback string) string {
	if fallback != "" {
		fmt.Printf("%s [%s]: ", p, fallback)
	} else {
		fmt.Printf("%s: ", p)
	}
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		// Return unmasked original value
		return strings.TrimPrefix(fallback, "****")
	}
	return line
}

func promptInt(reader *bufio.Reader, p string, min int, max int) int {
	for {
		fmt.Printf("%s (%d-%d): ", p, min, max)
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		var n int
		if _, err := fmt.Sscanf(line, "%d", &n); err == nil && n >= 0 && n <= max {
			return n
		}
		fmt.Println("  Invalid choice, try again.")
	}
}

func promptYesNo(reader *bufio.Reader, p string, defaultYes bool) bool {
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	fmt.Printf("%s %s: ", p, suffix)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return defaultYes
	}
	return line == "y" || line == "yes"
}

func generateToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "pol_" + hex.EncodeToString(b), nil
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

func defaultStr(val, fallback string) string {
	if val != "" {
		return val
	}
	return fallback
}
