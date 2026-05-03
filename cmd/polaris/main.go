package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/berkaycubuk/polaris-agent/internal/agent"
	"github.com/berkaycubuk/polaris-agent/internal/captioner"
	"github.com/berkaycubuk/polaris-agent/internal/config"
	"github.com/berkaycubuk/polaris-agent/internal/llm"
	"github.com/berkaycubuk/polaris-agent/internal/server"
	"github.com/berkaycubuk/polaris-agent/internal/storage"
	"github.com/berkaycubuk/polaris-agent/internal/telegram"
	"github.com/berkaycubuk/polaris-agent/internal/tools"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatalf("data dir: %v", err)
	}

	llmClient := llm.New(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
	registry := tools.NewRegistry(cfg.DataDir)

	opts := agent.Options{MaxToolIterations: cfg.MaxToolIterations}
	if cfg.ImageCaptionEnabled() {
		capClient := llm.New(cfg.ImageCaptionBaseURL, cfg.ImageCaptionAPIKey, cfg.ImageCaptionModel)
		opts.Captioner = captioner.New(capClient)
		log.Printf("image captioner enabled: %s @ %s", cfg.ImageCaptionModel, cfg.ImageCaptionBaseURL)
	} else {
		log.Printf("image captioner disabled (set IMAGE_CAPTION_* to enable)")
	}
	if cfg.R2Enabled() {
		opts.R2 = storage.New(cfg.R2AccountID, cfg.R2Bucket, cfg.R2AccessKeyID, cfg.R2SecretKey, cfg.R2PublicBaseURL)
		log.Printf("r2 storage enabled: bucket=%s", cfg.R2Bucket)
	} else {
		log.Printf("r2 storage disabled (set R2_* to enable)")
	}

	a := agent.New(llmClient, registry, cfg.DataDir, opts)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	errc := make(chan error, 2)

	go func() {
		s := server.New(cfg.HTTPAddr, cfg.AuthToken, a)
		errc <- s.Run(ctx)
	}()

	if cfg.TelegramBotToken != "" {
		go func() {
			b := telegram.New(cfg.TelegramBotToken, a)
			errc <- b.Run(ctx)
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
