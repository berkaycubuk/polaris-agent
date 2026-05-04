package scheduler

import (
	"strings"
	"testing"
	"time"
)

func TestParseSchedule_DurationOneShot(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"30m", 30 * time.Minute},
		{"2h", 2 * time.Hour},
		{"1d", 24 * time.Hour},
		{"45 minutes", 45 * time.Minute},
		{"3 hours", 3 * time.Hour},
		{"7 days", 7 * 24 * time.Hour},
	}
	now := time.Now()
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			s, err := ParseSchedule(c.in)
			if err != nil {
				t.Fatalf("parse %q: %v", c.in, err)
			}
			if s.Kind != "once" || s.RunAt == nil {
				t.Fatalf("expected one-shot with RunAt, got %+v", s)
			}
			delta := s.RunAt.Sub(now)
			tolerance := 5 * time.Second
			if delta < c.want-tolerance || delta > c.want+tolerance {
				t.Fatalf("RunAt off by more than %s: got delta=%s want=%s", tolerance, delta, c.want)
			}
		})
	}
}

func TestParseSchedule_Interval(t *testing.T) {
	s, err := ParseSchedule("every 2h")
	if err != nil {
		t.Fatal(err)
	}
	if s.Kind != "interval" || s.Interval != 2*time.Hour {
		t.Fatalf("unexpected: %+v", s)
	}
	if !strings.Contains(s.Display, "every") {
		t.Fatalf("display missing 'every': %q", s.Display)
	}
}

func TestParseSchedule_IntervalTooShort(t *testing.T) {
	if _, err := ParseSchedule("every 30s"); err == nil {
		t.Fatal("expected error for sub-minute interval")
	}
}

func TestParseSchedule_RFC3339(t *testing.T) {
	s, err := ParseSchedule("2030-01-15T09:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if s.Kind != "once" || s.RunAt == nil {
		t.Fatalf("expected one-shot, got %+v", s)
	}
	if s.RunAt.Year() != 2030 || s.RunAt.Month() != 1 || s.RunAt.Day() != 15 {
		t.Fatalf("unexpected RunAt: %v", s.RunAt)
	}
}

func TestParseSchedule_LocalTimestamp(t *testing.T) {
	// No timezone — accept and interpret as local.
	if _, err := ParseSchedule("2030-01-15T09:00:00"); err != nil {
		t.Fatalf("expected local-ts parse to succeed: %v", err)
	}
}

func TestParseSchedule_Invalid(t *testing.T) {
	for _, in := range []string{"", "tomorrow", "100", "every", "every 0m"} {
		if _, err := ParseSchedule(in); err == nil {
			t.Fatalf("expected error for %q", in)
		}
	}
}

func TestNextRun_Once(t *testing.T) {
	now := time.Now().UTC()
	future := now.Add(5 * time.Minute)
	s := Schedule{Kind: "once", RunAt: &future}

	if got := s.NextRun(time.Time{}, now); !got.Equal(future) {
		t.Fatalf("expected RunAt for unrun job, got %v", got)
	}
	if got := s.NextRun(now, now); !got.IsZero() {
		t.Fatalf("expected zero for already-run one-shot, got %v", got)
	}

	// Past the grace window → no future fire.
	past := now.Add(-30 * time.Minute)
	sPast := Schedule{Kind: "once", RunAt: &past}
	if got := sPast.NextRun(time.Time{}, now); !got.IsZero() {
		t.Fatalf("expected zero for missed one-shot, got %v", got)
	}

	// Within the grace window → still eligible.
	recent := now.Add(-30 * time.Second)
	sRecent := Schedule{Kind: "once", RunAt: &recent}
	if got := sRecent.NextRun(time.Time{}, now); got.IsZero() {
		t.Fatal("expected recent one-shot to still be eligible (grace)")
	}
}

func TestNextRun_Interval(t *testing.T) {
	now := time.Now().UTC()
	s := Schedule{Kind: "interval", Interval: 2 * time.Hour}

	// Never run → first run is now+interval.
	first := s.NextRun(time.Time{}, now)
	if first.Sub(now) != 2*time.Hour {
		t.Fatalf("expected now+2h, got delta %s", first.Sub(now))
	}

	// Already run → next is last+interval.
	last := now.Add(-30 * time.Minute)
	next := s.NextRun(last, now)
	if !next.Equal(last.Add(2 * time.Hour)) {
		t.Fatalf("expected last+2h, got %v", next)
	}
}
