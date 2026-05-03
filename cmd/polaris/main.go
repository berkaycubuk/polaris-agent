package main

import (
	"context"
	"fmt"
	"os"

	"github.com/berkaycubuk/polaris-agent/internal/chat"
	"github.com/berkaycubuk/polaris-agent/internal/doctor"
	"github.com/berkaycubuk/polaris-agent/internal/setup"
)

// Set via -ldflags at build time.
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "setup":
		if err := setup.Run(envPath()); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "doctor":
		verbose := false
		for _, arg := range os.Args[2:] {
			if arg == "-v" || arg == "--verbose" {
				verbose = true
			}
		}
		results := doctor.Run(context.Background(), envPath(), verbose)
		doctor.PrintResults(os.Stdout, results)
		for _, r := range results {
			if r.Status == "fail" {
				os.Exit(1)
			}
		}
	case "chat":
		opts := chat.LoadChatOpts(envPath())
		// Allow CLI flags to override
		for i := 2; i < len(os.Args); i++ {
			switch {
			case os.Args[i] == "--server" && i+1 < len(os.Args):
				opts.ServerURL = os.Args[i+1]
				i++
			case os.Args[i] == "--session" && i+1 < len(os.Args):
				opts.Session = os.Args[i+1]
				i++
			case os.Args[i] == "--token" && i+1 < len(os.Args):
				opts.AuthToken = os.Args[i+1]
				i++
			}
		}
		if opts.AuthToken == "" {
			fmt.Fprintln(os.Stderr, "error: AUTH_TOKEN not set — run: polaris setup")
			os.Exit(1)
		}
		if err := chat.Run(opts); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		printUsage()
	case "version", "-V", "--version":
		fmt.Printf("polaris %s\n", version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

// envPath determines the .env file location.
// Priority: POLARIS_ENV > .env in current directory.
func envPath() string {
	if p := os.Getenv("POLARIS_ENV"); p != "" {
		return p
	}
	return ".env"
}

func printUsage() {
	fmt.Println("polaris — personal AI companion CLI")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  polaris chat      Chat with your agent (interactive)")
	fmt.Println("  polaris setup     Configure the agent (interactive wizard)")
	fmt.Println("  polaris doctor    Diagnose configuration issues")
	fmt.Println("  polaris version   Show version")
	fmt.Println("  polaris help      Show this help message")
	fmt.Println()
	fmt.Println("Chat options:")
	fmt.Println("  --server <url>    Server URL (default: http://localhost:8080)")
	fmt.Println("  --session <id>    Session ID (default: cli)")
	fmt.Println("  --token <token>   Auth token (reads from .env by default)")
	fmt.Println()
	fmt.Println("The agent server runs inside Docker:")
	fmt.Println("  docker compose up -d    Start the agent")
	fmt.Println("  docker compose logs -f  View logs")
}
