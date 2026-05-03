package main

import (
	"context"
	"fmt"
	"os"

	"github.com/berkaycubuk/polaris-agent/internal/doctor"
	"github.com/berkaycubuk/polaris-agent/internal/setup"
)

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
	case "help", "-h", "--help":
		printUsage()
	case "version", "-V", "--version":
		fmt.Println("polaris dev")
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
	fmt.Println("  polaris setup     Configure the agent (interactive wizard)")
	fmt.Println("  polaris doctor    Diagnose configuration issues")
	fmt.Println("  polaris version   Show version")
	fmt.Println("  polaris help      Show this help message")
	fmt.Println()
	fmt.Println("The agent server runs inside Docker:")
	fmt.Println("  docker compose up -d    Start the agent")
	fmt.Println("  docker compose logs -f  View logs")
}
