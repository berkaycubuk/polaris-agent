// Package chat provides an interactive terminal chat client that connects
// to a running Polaris Agent server.
package chat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
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

// ANSI styles. Empty when stdout isn't a TTY so logs/pipes stay clean.
var (
	cyan, dim, reset, red, green string
	tty                          bool
)

func init() {
	if isTTY(os.Stdout) {
		tty = true
		cyan = "\033[36m"
		dim = "\033[2m"
		reset = "\033[0m"
		red = "\033[31m"
		green = "\033[32m"
	}
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// Run starts the interactive chat loop. It blocks until the user exits.
func Run(opts Options) error {
	if opts.ServerURL == "" {
		opts.ServerURL = "http://localhost:8080"
	}
	if opts.Session == "" {
		opts.Session = "cli"
	}

	if err := healthCheck(opts.ServerURL); err != nil {
		return fmt.Errorf("server not reachable at %s: %w", opts.ServerURL, err)
	}

	printBanner(opts)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		if rawCleanup != nil {
			rawCleanup()
		}
		fmt.Print("\n  Bye!\n\n")
		os.Exit(0)
	}()

	prompt := fmt.Sprintf("%s  ❯ %s", cyan, reset)
	var history []string

	for {
		line, err := readLine(prompt, history)
		if errors.Is(err, io.EOF) {
			fmt.Print("  Bye!\n\n")
			return nil
		}
		if errors.Is(err, errInterrupted) {
			continue
		}
		if err != nil {
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if n := len(history); n == 0 || history[n-1] != line {
			history = append(history, line)
		}

		handled, exit := handleCommand(line, opts)
		if exit {
			return nil
		}
		if handled {
			continue
		}

		if err := submitMessage(opts, line); err != nil {
			fmt.Fprintf(os.Stderr, "  %s✗%s %v\n\n", red, reset, err)
		}
	}
}

// banner is the figlet "Small Slant" rendering of "POLARIS".
const banner = `     ___  ____  __   ___   ___  ________
    / _ \/ __ \/ /  / _ | / _ \/  _/ __/
   / ___/ /_/ / /__/ __ |/ , _// /_\ \
  /_/   \____/____/_/ |_/_/|_/___/___/`

func printBanner(opts Options) {
	fmt.Print("\n")
	fmt.Printf("%s%s%s\n", cyan, banner, reset)
	fmt.Print("\n")
	fmt.Printf("  Server:  %s%s%s\n", dim, opts.ServerURL, reset)
	fmt.Printf("  Session: %s%s%s\n", dim, opts.Session, reset)
	fmt.Print("\n")
	fmt.Printf("  %s/help for commands · /quit to exit%s\n", dim, reset)
	fmt.Print("\n")
}

// handleCommand processes slash commands. Returns (handled, shouldExit).
func handleCommand(line string, opts Options) (handled bool, exit bool) {
	if !strings.HasPrefix(line, "/") {
		return false, false
	}
	fields := strings.Fields(line)
	cmd := strings.ToLower(fields[0])
	switch cmd {
	case "/quit", "/exit", "/q":
		fmt.Print("  Bye!\n\n")
		return true, true
	case "/reset":
		if err := resetSession(opts); err != nil {
			fmt.Fprintf(os.Stderr, "  %s✗%s reset failed: %v\n\n", red, reset, err)
		} else {
			fmt.Printf("  %s✓%s session reset\n\n", green, reset)
		}
		return true, false
	case "/help":
		printHelp()
		return true, false
	default:
		fmt.Printf("  unknown command: %s\n", cmd)
		printHelp()
		return true, false
	}
}

func submitMessage(opts Options, message string) error {
	fmt.Println()
	sp := newSpinner()
	sp.start()

	reply, err := sendMessage(opts, message)
	sp.stop()

	if err != nil {
		return err
	}
	printReply(reply)
	return nil
}

// printReply prints the agent's reply with indented, rune-aware wrapping.
func printReply(reply string) {
	const indent = "  "
	width := wrapWidth()
	fmt.Printf("  %sPolaris%s\n", cyan, reset)
	for _, line := range strings.Split(strings.TrimRight(reply, "\n"), "\n") {
		if line == "" {
			fmt.Println()
			continue
		}
		for _, wl := range wrapLine(line, width, len(indent)) {
			fmt.Println(indent + wl)
		}
	}
	fmt.Println()
}

// wrapWidth returns the desired total line width.
func wrapWidth() int {
	if v := os.Getenv("COLUMNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 20 {
			if n > 100 {
				return 100
			}
			return n
		}
	}
	if w, ok := termCols(); ok {
		if w > 100 {
			return 100
		}
		if w < 40 {
			return 40
		}
		return w
	}
	return 80
}

// wrapLine wraps a string to fit within (width - indentLen) runes per line,
// breaking on the last space when possible. Rune-aware.
func wrapLine(line string, width int, indentLen int) []string {
	effective := width - indentLen
	if effective < 20 {
		effective = 20
	}
	runes := []rune(line)
	if len(runes) <= effective {
		return []string{line}
	}
	var out []string
	for len(runes) > effective {
		split := effective
		for i := effective; i > 0; i-- {
			if runes[i] == ' ' {
				split = i
				break
			}
		}
		out = append(out, strings.TrimRight(string(runes[:split]), " "))
		j := split
		for j < len(runes) && runes[j] == ' ' {
			j++
		}
		runes = runes[j:]
	}
	if len(runes) > 0 {
		out = append(out, string(runes))
	}
	return out
}

func printHelp() {
	fmt.Print("\n")
	fmt.Println("  Commands:")
	fmt.Println("    /reset   Clear session history")
	fmt.Println("    /help    Show this help")
	fmt.Println("    /quit    Exit the chat")
	fmt.Print("\n")
}

// --- HTTP ----------------------------------------------------------------

func healthCheck(serverURL string) error {
	url := strings.TrimRight(serverURL, "/") + "/healthz"
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
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
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
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
	defer func() { _ = resp.Body.Close() }()

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

// --- Spinner --------------------------------------------------------------

// spinner shows a non-blocking progress indicator on its own line. It
// repaints in place so the line never grows; stop() clears the line so the
// reply renders cleanly from column 0.
type spinner struct {
	stopCh chan struct{}
	doneCh chan struct{}
	once   sync.Once
}

func newSpinner() *spinner {
	return &spinner{
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

func (s *spinner) start() {
	if !tty {
		// Pipes/non-tty: skip animation; print one static "thinking" line.
		fmt.Println("  thinking...")
		close(s.doneCh)
		return
	}
	go func() {
		defer close(s.doneCh)
		frames := []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		s.paint(frames[0])
		i := 0
		for {
			select {
			case <-s.stopCh:
				return
			case <-ticker.C:
				i++
				s.paint(frames[i%len(frames)])
			}
		}
	}()
}

func (s *spinner) paint(r rune) {
	fmt.Printf("\r\033[K  %s%c thinking...%s", dim, r, reset)
}

func (s *spinner) stop() {
	s.once.Do(func() { close(s.stopCh) })
	<-s.doneCh
	if tty {
		fmt.Print("\r\033[K")
	}
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
