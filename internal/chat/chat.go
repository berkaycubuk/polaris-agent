// Package chat provides an interactive terminal chat client that connects
// to a running Polaris Agent server.
package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/berkaycubuk/polaris-agent/internal/doctor"
)

// Options configures the chat client.
type Options struct {
	ServerURL string // base URL of the server (e.g. "http://localhost:8080")
	AuthToken string // Bearer token for authentication
	Session   string // session ID (default "cli")
}

// slashCmd defines a slash command shown in the autocomplete menu.
type slashCmd struct {
	Name string
	Desc string
}

var slashCommands = []slashCmd{
	{"/reset", "Clear session history"},
	{"/quit", "Exit the chat"},
}

// Run starts the interactive chat loop. It blocks until the user exits.
func Run(opts Options) error {
	if opts.ServerURL == "" {
		opts.ServerURL = "http://localhost:8080"
	}
	if opts.Session == "" {
		opts.Session = "cli"
	}

	// Health check first
	if err := healthCheck(opts.ServerURL); err != nil {
		return fmt.Errorf("server not reachable at %s: %w", opts.ServerURL, err)
	}

	fmt.Print("\n")
	fmt.Println("  ╔═══════════════════════════════════════════╗")
	fmt.Println("  ║       ★ Polaris Chat ★                    ║")
	fmt.Println("  ╚═══════════════════════════════════════════╝")
	fmt.Print("\n")
	fmt.Printf("  Server:  %s\n", opts.ServerURL)
	fmt.Printf("  Session: %s\n", opts.Session)
	fmt.Print("\n")
	fmt.Println("  Type your message and press Enter. Type / for commands.")
	fmt.Print("\n")

	// Put terminal in raw mode for character-by-character input
	oldState, err := makeRaw()
	if err != nil {
		// Fallback: terminal doesn't support raw mode (e.g. piped input)
		return runLineBuffered(opts)
	}
	defer restore(oldState)

	// Handle Ctrl+C and terminal resize
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		restore(oldState)
		fmt.Print("\n\n  Bye! 👋\n\n")
		os.Exit(0)
	}()

	return runRaw(opts)
}

// runRaw handles character-by-character input with inline slash command
// autocomplete. The terminal is already in raw mode.
func runRaw(opts Options) error {
	var buf []rune
	menuVisible := false
	menuIdx := 0

	for {
		promptLine := "  ❯ " + string(buf)
		fmt.Print("\r\033[K") // clear current line
		fmt.Print(promptLine)

		if menuVisible {
			filtered := filterCommands(buf)
			menuIdx = clamp(menuIdx, 0, len(filtered)-1)
			printMenu(filtered, menuIdx)
		}

		r, err := readRune()
		if err != nil {
			if err == io.EOF {
				fmt.Print("\n\n  Bye! 👋\n\n")
				return nil
			}
			return fmt.Errorf("read input: %w", err)
		}

		switch {
		case r == ctrlC:
			fmt.Print("\n\n  Bye! 👋\n\n")
			return nil

		case r == enter:
			if menuVisible {
				// Accept the selected completion
				filtered := filterCommands(buf)
				if len(filtered) > 0 {
					menuIdx = clamp(menuIdx, 0, len(filtered)-1)
					buf = []rune(filtered[menuIdx].Name + " ")
				}
				clearMenu(len(filtered))
				menuVisible = false
				menuIdx = 0
				continue
			}

			// Submit line
			fmt.Print("\n")
			line := string(buf)
			buf = buf[:0]

			handled, exit := handleCommand(line, opts)
			if exit {
				return nil
			}
			if !handled {
				if strings.TrimSpace(line) != "" {
					if err := submitMessage(opts, line); err != nil {
						fmt.Fprintf(os.Stderr, "  ✗ error: %v\n\n", err)
					}
				}
			}

		case r == tab:
			if menuVisible {
				// Move selection down
				filtered := filterCommands(buf)
				if len(filtered) > 0 {
					menuIdx = (menuIdx + 1) % len(filtered)
				}
			}

		case r == esc:
			// Read ANSI escape sequence
			seq := readEscapeSeq()
			if seq == "up" && menuVisible {
				filtered := filterCommands(buf)
				if len(filtered) > 0 {
					menuIdx--
					if menuIdx < 0 {
						menuIdx = len(filtered) - 1
					}
				}
			} else if seq == "down" && menuVisible {
				filtered := filterCommands(buf)
				if len(filtered) > 0 {
					menuIdx = (menuIdx + 1) % len(filtered)
				}
			} else if seq != "" {
				// Unknown escape — ignore
			} else {
				// Bare escape — close menu
				if menuVisible {
					filtered := filterCommands(buf)
					clearMenu(len(filtered))
					menuVisible = false
					menuIdx = 0
				}
			}

		case r == backspace || r == del:
			if len(buf) > 0 {
				if menuVisible {
					filtered := filterCommands(buf)
					clearMenu(len(filtered))
				}
				buf = buf[:len(buf)-1]
				if len(buf) == 0 || buf[0] != '/' {
					menuVisible = false
					menuIdx = 0
				} else {
					filtered := filterCommands(buf)
					if len(filtered) == 0 {
						menuVisible = false
					} else {
						menuVisible = true
						menuIdx = clamp(menuIdx, 0, len(filtered)-1)
					}
				}
			}

		case r >= ' ' && r < 127:
			if menuVisible {
				filtered := filterCommands(buf)
				clearMenu(len(filtered))
			}
			buf = append(buf, r)

			// Show menu when typing starts with /
			if len(buf) == 1 && buf[0] == '/' {
				menuVisible = true
				menuIdx = 0
			} else if len(buf) > 1 && buf[0] == '/' && menuVisible {
				filtered := filterCommands(buf)
				if len(filtered) == 0 {
					menuVisible = false
				} else {
					menuIdx = clamp(menuIdx, 0, len(filtered)-1)
				}
			} else {
				menuVisible = false
			}

		default:
			// Ignore other control characters
		}
	}
}

// runLineBuffered is a fallback for terminals that don't support raw mode.
func runLineBuffered(opts Options) error {
	var buf [1024]byte
	for {
		fmt.Print("  ❯ ")
		n, err := os.Stdin.Read(buf[:])
		if err != nil {
			if err == io.EOF {
				fmt.Print("\n\n  Bye! 👋\n\n")
				return nil
			}
			return fmt.Errorf("read input: %w", err)
		}
		line := strings.TrimSpace(string(buf[:n]))
		if line == "" {
			continue
		}

		handled, exit := handleCommand(line, opts)
		if exit {
			return nil
		}
		if !handled {
			if err := submitMessage(opts, line); err != nil {
				fmt.Fprintf(os.Stderr, "  ✗ error: %v\n\n", err)
			}
		}
	}
}

// handleCommand processes slash commands. Returns (handled, shouldExit).
func handleCommand(line string, opts Options) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "/quit", "/exit", "/q":
		fmt.Print("  Bye! 👋\n\n")
		return true, true
	case "/reset":
		if err := resetSession(opts); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ reset failed: %v\n\n", err)
		} else {
			fmt.Print("  ✓ Session reset.\n\n")
		}
		return true, false
	case "/help":
		printHelp()
		return true, false
	}

	// Partial /reset or /quit with trailing space etc.
	trimmed := strings.ToLower(strings.Fields(line)[0])
	if trimmed == "/reset" || trimmed == "/quit" || trimmed == "/exit" {
		return handleCommand(trimmed, opts)
	}
	return false, false
}

func submitMessage(opts Options, message string) error {
	fmt.Print("  ⏳ ")
	spinner := newSpinner()
	go spinner.start()

	reply, err := sendMessage(opts, message)
	spinner.stop()

	if err != nil {
		fmt.Fprintf(os.Stderr, "\r  ✗ error: %v\n\n", err)
		return err
	}

	fmt.Printf("\r  \033[K") // clear spinner line
	printReply(reply)
	return nil
}

// filterCommands returns slash commands matching the current input.
func filterCommands(buf []rune) []slashCmd {
	input := string(buf)
	var matched []slashCmd
	for _, cmd := range slashCommands {
		if strings.HasPrefix(cmd.Name, input) {
			matched = append(matched, cmd)
		}
	}
	return matched
}

// printMenu renders the autocomplete menu below the input line.
func printMenu(cmds []slashCmd, selected int) {
	// Save cursor, move to next line, print menu, restore cursor
	fmt.Print("\n\033[s") // newline + save cursor
	for i, cmd := range cmds {
		if i == selected {
			fmt.Printf("\033[K  \033[7m %-10s %s\033[0m\n", cmd.Name, cmd.Desc)
		} else {
			fmt.Printf("\033[K   %-10s %s\n", cmd.Name, cmd.Desc)
		}
	}
	fmt.Print("\033[u") // restore cursor to input line
}

// clearMenu erases a previously rendered menu of n items.
func clearMenu(n int) {
	if n == 0 {
		return
	}
	// Move down 1 line (to menu start), clear n+1 lines, move back up
	fmt.Printf("\033[%dB", 1)   // down 1
	for i := 0; i < n; i++ {
		fmt.Print("\033[K\033[A") // clear line, move up
	}
	fmt.Print("\033[K")           // clear the last line
	fmt.Printf("\033[%dA", 1)     // move back up to input line
}

func printReply(reply string) {
	const width = 76
	indent := "  "
	lines := strings.Split(reply, "\n")
	for _, line := range lines {
		if line == "" {
			fmt.Println()
			continue
		}
		wrapped := wrapLine(line, width, len(indent))
		for _, wl := range wrapped {
			fmt.Println(indent + wl)
		}
	}
	fmt.Println()
}

func wrapLine(line string, width int, indentLen int) []string {
	effectiveWidth := width - indentLen
	if effectiveWidth < 20 {
		effectiveWidth = 20
	}

	var result []string
	cur := line
	for len(cur) > effectiveWidth {
		spaceIdx := strings.LastIndex(cur[:effectiveWidth], " ")
		if spaceIdx <= 0 {
			spaceIdx = effectiveWidth
		}
		result = append(result, cur[:spaceIdx])
		cur = strings.TrimLeft(cur[spaceIdx:], " ")
	}
	if cur != "" {
		result = append(result, cur)
	}
	return result
}

func printHelp() {
	fmt.Print("\n")
	fmt.Println("  Commands:")
	fmt.Println("    /reset   Clear session history")
	fmt.Println("    /quit    Exit the chat")
	fmt.Println("    /help    Show this help")
	fmt.Print("\n")
}

// --- HTTP functions ---

// --- HTTP functions ---

func healthCheck(serverURL string) error {
	url := strings.TrimRight(serverURL, "/") + "/healthz"
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func sendMessage(opts Options, message string) (string, error) {
	url := strings.TrimRight(opts.ServerURL, "/") + "/chat"

	body := chatReq{Session: opts.Session, Message: message}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+opts.AuthToken)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("unauthorized — check AUTH_TOKEN in .env")
	}
	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return "", fmt.Errorf("server error: %s", errResp.Error)
		}
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp struct {
		Reply string `json:"reply"`
	}
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	return chatResp.Reply, nil
}

func resetSession(opts Options) error {
	url := strings.TrimRight(opts.ServerURL, "/") + "/reset"

	body := chatReq{Session: opts.Session}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+opts.AuthToken)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("unauthorized")
	}
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

type chatReq struct {
	Session string `json:"session"`
	Message string `json:"message,omitempty"`
}

// spinner shows a simple animation while waiting.
type spinner struct {
	frames []string
	stopCh chan struct{}
}

func newSpinner() *spinner {
	return &spinner{
		frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		stopCh: make(chan struct{}),
	}
}

func (s *spinner) start() {
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	i := 0
	for {
		select {
		case <-ticker.C:
			frame := s.frames[i%len(s.frames)]
			fmt.Printf("\r  %s thinking...", frame)
			i++
		case <-s.stopCh:
			return
		}
	}
}

func (s *spinner) stop() {
	close(s.stopCh)
}

// LoadChatOpts reads the .env file and returns Options for the chat client.
// Falls back to sensible defaults for missing values.
func LoadChatOpts(envPath string) Options {
	vars := map[string]string{}
	if f, err := os.Open(envPath); err == nil {
		vars = doctor.ParseEnvFileFromReader(f)
		_ = f.Close()
	}

	serverURL := vars["POLARIS_SERVER_URL"]
	if serverURL == "" {
		// Derive from HTTP_ADDR — if it starts with ":", prepend "http://localhost"
		httpAddr := vars["HTTP_ADDR"]
		if httpAddr == "" {
			httpAddr = ":8080"
		}
		if strings.HasPrefix(httpAddr, ":") {
			serverURL = "http://localhost" + httpAddr
		} else {
			serverURL = "http://" + httpAddr
		}
	}

	return Options{
		ServerURL: serverURL,
		AuthToken: vars["AUTH_TOKEN"],
		Session:   vars["POLARIS_SESSION"],
	}
}
