package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/berkaycubuk/polaris-agent/internal/agent"
	"github.com/berkaycubuk/polaris-agent/internal/attachment"
	"github.com/berkaycubuk/polaris-agent/internal/captioner"
	"github.com/berkaycubuk/polaris-agent/internal/config"
	"github.com/berkaycubuk/polaris-agent/internal/llm"
	"github.com/berkaycubuk/polaris-agent/internal/scheduler"
	"github.com/berkaycubuk/polaris-agent/internal/server"
	"github.com/berkaycubuk/polaris-agent/internal/snapshot"
	"github.com/berkaycubuk/polaris-agent/internal/storage"
	"github.com/berkaycubuk/polaris-agent/internal/telegram"
	"github.com/berkaycubuk/polaris-agent/internal/tools"
)

// agentRunner adapts *agent.Agent to scheduler.Runner. The scheduler doesn't
// pass attachments, so the variadic argument is left empty.
type agentRunner struct{ a *agent.Agent }

func (r agentRunner) Chat(ctx context.Context, sessionID, message string) (string, error) {
	return r.a.Chat(ctx, sessionID, message)
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	serve()
}

func serve() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n\n", err)
		fmt.Fprintf(os.Stderr, "Run 'polaris setup' to configure, or 'polaris doctor' to diagnose.\n")
		os.Exit(1)
	}

	if err := seedDataDir(cfg.DataDir); err != nil {
		log.Fatalf("seed data dir: %v", err)
	}

	llmClient := llm.New(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
	registry := tools.NewRegistry(cfg.DataDir)
	if names := registry.SkillEnvNames(); len(names) > 0 {
		log.Printf("skill secret env vars (%d): %v", len(names), names)
	} else {
		log.Printf("no SKILL_* (secret) env vars detected")
	}
	if names := registry.PublicEnvNames(); len(names) > 0 {
		log.Printf("skill public env vars (%d): %v", len(names), names)
	}
	if files := registry.SecretsFiles(); len(files) > 0 {
		log.Printf("secrets files detected (%d): %v", len(files), files)
	}

	var proc *attachment.Processor
	{
		var cap *captioner.Captioner
		if cfg.ImageCaptionEnabled() {
			capClient := llm.New(cfg.ImageCaptionBaseURL, cfg.ImageCaptionAPIKey, cfg.ImageCaptionModel)
			cap = captioner.New(capClient)
			log.Printf("image captioner enabled: %s @ %s", cfg.ImageCaptionModel, cfg.ImageCaptionBaseURL)
		} else {
			log.Printf("image captioner disabled (set IMAGE_CAPTION_* to enable)")
		}
		var r2 *storage.R2
		if cfg.R2Enabled() {
			r2 = storage.New(cfg.R2AccountID, cfg.R2Bucket, cfg.R2AccessKeyID, cfg.R2SecretKey, cfg.R2PublicBaseURL)
			log.Printf("r2 storage enabled: bucket=%s", cfg.R2Bucket)
		} else {
			log.Printf("r2 storage disabled (set R2_* to enable)")
		}
		proc = attachment.NewProcessor(cap, r2)
	}
	snap := snapshot.New(cfg.DataDir, cfg.GitEnabled)
	if snap.Enabled() {
		log.Printf("snapshots enabled: per-turn git commits in %s", cfg.DataDir)
	} else {
		log.Printf("snapshots disabled (GIT_ENABLED=false or git binary missing)")
	}
	opts := agent.Options{
		MaxToolIterations: cfg.MaxToolIterations,
		MaxHistoryChars:   cfg.MaxHistoryChars,
		Processor:         proc,
		Snapshotter:       snap,
	}

	a := agent.New(llmClient, registry, cfg.DataDir, opts)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	errc := make(chan error, 3)

	// Telegram bot constructed before scheduler so the deliverer can push
	// cron-job replies to Telegram chats. Scheduler is wired regardless;
	// non-Telegram origins still get per-run output saved to disk.
	var tgBot *telegram.Bot
	if cfg.TelegramBotToken != "" {
		ownerFile := filepath.Join(cfg.DataDir, ".telegram-owner")
		tgBot = telegram.New(cfg.TelegramBotToken, a, cfg.TelegramAllowedIDs, ownerFile)
	}

	store, err := scheduler.NewStore(cfg.DataDir)
	if err != nil {
		log.Fatalf("scheduler store: %v", err)
	}
	deliverer := scheduler.NewFanoutDeliverer(filepath.Join(store.Dir(), "output"), tgBot)
	scriptRunner := scheduler.NewExecScriptRunner(cfg.DataDir, registry.ChildEnv(), 0)
	sched := scheduler.New(store, agentRunner{a}, scriptRunner, deliverer, 0, 0)
	registry.EnableScheduler(store, sched)
	log.Printf("scheduler ready: %d job(s) loaded", len(store.List()))

	go func() {
		errc <- sched.Run(ctx)
	}()

	go func() {
		s := server.New(cfg.HTTPAddr, cfg.AuthToken, a)
		errc <- s.Run(ctx)
	}()

	if tgBot != nil {
		go func() {
			errc <- tgBot.Run(ctx)
		}()
	}

	select {
	case <-ctx.Done():
		log.Printf("shutting down")
	case err := <-errc:
		if err != nil && err != context.Canceled {
			log.Printf("subsystem exited: %v", err)
		}
	}
}

// seedDataDir creates the data directory structure and writes default files
// if they don't already exist. This runs on every server startup.
func seedDataDir(dataDir string) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	for _, sub := range []string{"wiki", "skills", "secrets"} {
		if err := os.MkdirAll(filepath.Join(dataDir, sub), 0o755); err != nil {
			return fmt.Errorf("create data/%s: %w", sub, err)
		}
	}

	soulPath := filepath.Join(dataDir, "SOUL.md")
	if _, err := os.Stat(soulPath); err != nil {
		defaultSoul := `You are Polaris, a personal AI companion that adapts and grows with your user.
You have a persistent identity stored in SOUL.md and you learn about your user over time.
You take notes, do research, and tinker on ideas alongside your user.
You build up knowledge in a personal wiki of markdown files. When you learn something
worth remembering — facts about your user, recurring topics, project context — write it
to a wiki file under wiki/ so future-you can find it. Search the wiki before you assume
you don't know something.
Be candid, curious, and concise.
`
		if err := os.WriteFile(soulPath, []byte(defaultSoul), 0o644); err != nil {
			return fmt.Errorf("write SOUL.md: %w", err)
		}
	}

	return nil
}
