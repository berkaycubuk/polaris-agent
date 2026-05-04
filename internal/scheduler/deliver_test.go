package scheduler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeTG struct {
	chatID int64
	text   string
	err    error
}

func (f *fakeTG) Send(_ context.Context, chatID int64, text string) error {
	f.chatID = chatID
	f.text = text
	return f.err
}

func TestDeliverer_WritesOutput(t *testing.T) {
	dir := t.TempDir()
	d := NewFanoutDeliverer(dir, nil)
	job := Job{ID: "job_x", Name: "test", Origin: "cli", Schedule: Schedule{Display: "once"}, Prompt: "do it"}

	if err := d.Deliver(context.Background(), job, "result", nil); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "job_x", "*.md"))
	if len(files) != 1 {
		t.Fatalf("expected 1 output file, got %d (%v)", len(files), files)
	}
	body, _ := os.ReadFile(files[0])
	for _, want := range []string{"job_x", "do it", "result", "schedule: once"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("output missing %q\n%s", want, body)
		}
	}
}

func TestDeliverer_TelegramOriginPushes(t *testing.T) {
	tg := &fakeTG{}
	d := NewFanoutDeliverer(t.TempDir(), tg)
	job := Job{ID: "j1", Name: "ping", Origin: "telegram:42"}

	if err := d.Deliver(context.Background(), job, "hello", nil); err != nil {
		t.Fatal(err)
	}
	if tg.chatID != 42 {
		t.Fatalf("chatID = %d, want 42", tg.chatID)
	}
	if !strings.Contains(tg.text, "hello") {
		t.Fatalf("text = %q, want to contain 'hello'", tg.text)
	}
	if !strings.Contains(tg.text, "[ping]") {
		t.Fatalf("text should include name header, got %q", tg.text)
	}
}

func TestDeliverer_NonTelegramOriginNoSend(t *testing.T) {
	tg := &fakeTG{}
	d := NewFanoutDeliverer(t.TempDir(), tg)
	job := Job{ID: "j1", Origin: "cli"}
	if err := d.Deliver(context.Background(), job, "x", nil); err != nil {
		t.Fatal(err)
	}
	if tg.text != "" {
		t.Fatalf("expected no Telegram push for cli origin, got %q", tg.text)
	}
}

func TestDeliverer_RunErrorSuppressesPush(t *testing.T) {
	tg := &fakeTG{}
	d := NewFanoutDeliverer(t.TempDir(), tg)
	job := Job{ID: "j1", Origin: "telegram:42"}
	if err := d.Deliver(context.Background(), job, "partial", errors.New("boom")); err != nil {
		t.Fatal(err)
	}
	if tg.text != "" {
		t.Fatalf("run error should suppress push, got %q", tg.text)
	}
	// But output file is still written.
	files, _ := filepath.Glob(filepath.Join(d.dir, "j1", "*.md"))
	if len(files) != 1 {
		t.Fatalf("expected output file even on error, got %d", len(files))
	}
}

func TestDeliverer_EmptyReplySuppressesPush(t *testing.T) {
	tg := &fakeTG{}
	d := NewFanoutDeliverer(t.TempDir(), tg)
	job := Job{ID: "j1", Origin: "telegram:42"}
	if err := d.Deliver(context.Background(), job, "   ", nil); err != nil {
		t.Fatal(err)
	}
	if tg.text != "" {
		t.Fatalf("empty reply should suppress push, got %q", tg.text)
	}
}

func TestParseTelegramOrigin(t *testing.T) {
	if id, ok := parseTelegramOrigin("telegram:123"); !ok || id != 123 {
		t.Fatalf("expected (123, true), got (%d, %v)", id, ok)
	}
	if _, ok := parseTelegramOrigin("cli"); ok {
		t.Fatal("cli should not parse as telegram")
	}
	if _, ok := parseTelegramOrigin("telegram:abc"); ok {
		t.Fatal("non-numeric chat_id should not parse")
	}
}

// ensure unused; future-proof.
var _ = time.Second
