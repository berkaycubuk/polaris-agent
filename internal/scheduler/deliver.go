package scheduler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// TelegramSender is the minimal Telegram surface the deliverer needs. The
// scheduler package stays decoupled from the telegram package; cmd/server
// wires the real *telegram.Bot in.
type TelegramSender interface {
	Send(ctx context.Context, chatID int64, text string) error
}

// FanoutDeliverer writes every fired job's reply to disk and, when the
// origin is a Telegram session, pushes the reply to that chat.
type FanoutDeliverer struct {
	dir      string         // directory to write per-run output (typically <dataDir>/schedule/output)
	telegram TelegramSender // optional
}

// NewFanoutDeliverer builds a deliverer that writes output under outputDir.
// telegram may be nil — in that case Telegram-origin jobs are saved to disk
// only.
func NewFanoutDeliverer(outputDir string, tg TelegramSender) *FanoutDeliverer {
	return &FanoutDeliverer{dir: outputDir, telegram: tg}
}

func (d *FanoutDeliverer) Deliver(ctx context.Context, job Job, reply string, runErr error) error {
	if err := d.writeOutput(job, reply, runErr); err != nil {
		// Output write failure is logged but doesn't block Telegram push.
		fmt.Fprintf(os.Stderr, "scheduler: write output for %s: %v\n", job.ID, err)
	}

	// Don't push the run-error itself to the user — they didn't ask for it
	// and it leaks internal noise. The disk record is enough.
	if runErr != nil || strings.TrimSpace(reply) == "" {
		return nil
	}

	if chatID, ok := parseTelegramOrigin(job.Origin); ok && d.telegram != nil {
		header := ""
		if job.Name != "" {
			header = "[" + job.Name + "]\n"
		}
		return d.telegram.Send(ctx, chatID, header+reply)
	}
	return nil
}

func (d *FanoutDeliverer) writeOutput(job Job, reply string, runErr error) error {
	jobDir := filepath.Join(d.dir, job.ID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		return err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	path := filepath.Join(jobDir, stamp+".md")
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", firstNonEmpty(job.Name, job.ID))
	fmt.Fprintf(&b, "- job_id: %s\n", job.ID)
	fmt.Fprintf(&b, "- origin: %s\n", job.Origin)
	fmt.Fprintf(&b, "- schedule: %s\n", job.Schedule.Display)
	fmt.Fprintf(&b, "- fired_at: %s\n", time.Now().UTC().Format(time.RFC3339))
	if runErr != nil {
		fmt.Fprintf(&b, "- error: %s\n", runErr.Error())
	}
	b.WriteString("\n## prompt\n\n")
	b.WriteString(job.Prompt)
	b.WriteString("\n\n## reply\n\n")
	b.WriteString(reply)
	b.WriteString("\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func parseTelegramOrigin(origin string) (int64, bool) {
	const prefix = "telegram:"
	if !strings.HasPrefix(origin, prefix) {
		return 0, false
	}
	id, err := strconv.ParseInt(origin[len(prefix):], 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
