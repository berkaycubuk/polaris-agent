package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	LLMBaseURL        string
	LLMModel          string
	LLMAPIKey         string
	AuthToken         string
	TelegramBotToken  string
	DataDir           string
	HTTPAddr          string
	MaxToolIterations int

	// Image captioner. All three must be set together to enable image
	// understanding; if unset, image attachments are ignored with a notice.
	ImageCaptionBaseURL string
	ImageCaptionModel   string
	ImageCaptionAPIKey  string

	// Cloudflare R2 (S3-compatible) for long-term image storage. All five
	// fields must be set together to enable uploads. If unset, images are
	// captioned but the original bytes are dropped after the call.
	R2AccountID     string
	R2Bucket        string
	R2AccessKeyID   string
	R2SecretKey     string
	R2PublicBaseURL string // optional even when R2 is configured
}

func Load() (*Config, error) {
	loadDotEnv(".env")

	c := &Config{
		LLMBaseURL:        os.Getenv("LLM_BASE_URL"),
		LLMModel:          os.Getenv("LLM_MODEL"),
		LLMAPIKey:         os.Getenv("LLM_API_KEY"),
		AuthToken:         os.Getenv("AUTH_TOKEN"),
		TelegramBotToken:  os.Getenv("TELEGRAM_BOT_TOKEN"),
		DataDir:           getenvDefault("DATA_DIR", "/app/data"),
		HTTPAddr:          getenvDefault("HTTP_ADDR", ":8080"),
		MaxToolIterations: getenvInt("MAX_TOOL_ITERATIONS", 30),

		ImageCaptionBaseURL: os.Getenv("IMAGE_CAPTION_BASE_URL"),
		ImageCaptionModel:   os.Getenv("IMAGE_CAPTION_MODEL"),
		ImageCaptionAPIKey:  os.Getenv("IMAGE_CAPTION_API_KEY"),

		R2AccountID:     os.Getenv("R2_ACCOUNT_ID"),
		R2Bucket:        os.Getenv("R2_BUCKET"),
		R2AccessKeyID:   os.Getenv("R2_ACCESS_KEY_ID"),
		R2SecretKey:     os.Getenv("R2_SECRET_ACCESS_KEY"),
		R2PublicBaseURL: os.Getenv("R2_PUBLIC_BASE_URL"),
	}

	var missing []string
	if c.LLMBaseURL == "" {
		missing = append(missing, "LLM_BASE_URL")
	}
	if c.LLMModel == "" {
		missing = append(missing, "LLM_MODEL")
	}
	if c.LLMAPIKey == "" {
		missing = append(missing, "LLM_API_KEY")
	}
	if c.AuthToken == "" {
		missing = append(missing, "AUTH_TOKEN")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env: %s", strings.Join(missing, ", "))
	}

	if err := validateGroup("IMAGE_CAPTION_*",
		c.ImageCaptionBaseURL, c.ImageCaptionModel, c.ImageCaptionAPIKey); err != nil {
		return nil, err
	}
	if err := validateGroup("R2_*",
		c.R2AccountID, c.R2Bucket, c.R2AccessKeyID, c.R2SecretKey); err != nil {
		return nil, err
	}
	return c, nil
}

// ImageCaptionEnabled reports whether the captioner is configured.
func (c *Config) ImageCaptionEnabled() bool {
	return c.ImageCaptionBaseURL != "" && c.ImageCaptionModel != "" && c.ImageCaptionAPIKey != ""
}

// R2Enabled reports whether R2 storage is configured.
func (c *Config) R2Enabled() bool {
	return c.R2AccountID != "" && c.R2Bucket != "" && c.R2AccessKeyID != "" && c.R2SecretKey != ""
}

// validateGroup ensures that a set of related env vars is either all-set
// or all-empty. Half-configured groups are an error.
func validateGroup(label string, vals ...string) error {
	any, all := false, true
	for _, v := range vals {
		if v != "" {
			any = true
		} else {
			all = false
		}
	}
	if any && !all {
		return fmt.Errorf("%s is partially configured; set all related vars or none", label)
	}
	return nil
}

func getenvDefault(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func getenvInt(k string, d int) int {
	v := os.Getenv(k)
	if v == "" {
		return d
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return d
	}
	return n
}

func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		v = strings.Trim(v, `"'`)
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
		}
	}
}
