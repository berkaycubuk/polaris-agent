// Package scheduler runs background jobs that fire on a schedule and deliver
// their replies back to the origin session (Telegram chat, etc.).
package scheduler

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Schedule describes when a job fires.
type Schedule struct {
	// Kind is "once" or "interval".
	Kind string `json:"kind"`

	// RunAt is set when Kind=="once". Stored in UTC.
	RunAt *time.Time `json:"run_at,omitempty"`

	// Interval is set when Kind=="interval". Nanoseconds for JSON portability.
	Interval time.Duration `json:"interval,omitempty"`

	// Display is a human-readable form ("once in 30m", "every 2h", etc.).
	Display string `json:"display,omitempty"`
}

// onceshotGracePeriod gives a freshly-created one-shot job a small window
// to fire on the next tick even if creation lagged a few seconds past the
// requested time. Without grace, a "30m" scheduled at second 59 of a minute
// might miss the next tick.
const oneshotGracePeriod = 2 * time.Minute

// ParseSchedule accepts:
//   - "30m", "2h", "1d" — one-shot, fires N from now
//   - "every 30m", "every 2h" — recurring at fixed interval
//   - RFC3339 timestamp ("2026-02-03T14:00:00Z") — one-shot at T
//
// Cron expressions are intentionally not supported in v1. Add later if needed.
func ParseSchedule(s string) (Schedule, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return Schedule{}, fmt.Errorf("schedule is required")
	}
	lower := strings.ToLower(raw)

	if strings.HasPrefix(lower, "every ") {
		d, err := parseDuration(strings.TrimSpace(raw[6:]))
		if err != nil {
			return Schedule{}, fmt.Errorf("interval: %w", err)
		}
		if d < time.Minute {
			return Schedule{}, fmt.Errorf("interval must be at least 1m (got %s)", d)
		}
		return Schedule{
			Kind:     "interval",
			Interval: d,
			Display:  "every " + d.String(),
		}, nil
	}

	if strings.Contains(raw, "T") || isoDatePrefix.MatchString(raw) {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			// Try without zone — interpret as local.
			t, err = time.ParseInLocation("2006-01-02T15:04:05", raw, time.Local)
			if err != nil {
				t, err = time.ParseInLocation("2006-01-02T15:04", raw, time.Local)
				if err != nil {
					return Schedule{}, fmt.Errorf("invalid timestamp %q: %w", raw, err)
				}
			}
		}
		t = t.UTC()
		return Schedule{
			Kind:    "once",
			RunAt:   &t,
			Display: "once at " + t.Format("2006-01-02 15:04 MST"),
		}, nil
	}

	if d, err := parseDuration(raw); err == nil {
		t := time.Now().UTC().Add(d)
		return Schedule{
			Kind:    "once",
			RunAt:   &t,
			Display: "once in " + d.String(),
		}, nil
	}

	return Schedule{}, fmt.Errorf("invalid schedule %q. Use a duration (\"30m\", \"2h\", \"1d\"), \"every <duration>\", or an RFC3339 timestamp", raw)
}

var isoDatePrefix = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)

// parseDuration accepts "30m", "2h", "1d" with friendly aliases.
// Go's time.ParseDuration handles "30m"/"2h" but not "1d", so we wrap it.
var durationRe = regexp.MustCompile(`^(\d+)\s*(m|min|mins|minute|minutes|h|hr|hrs|hour|hours|d|day|days)$`)

func parseDuration(s string) (time.Duration, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	m := durationRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("invalid duration %q (use \"30m\", \"2h\", or \"1d\")", s)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("duration must be a positive integer")
	}
	switch m[2][0] {
	case 'm':
		return time.Duration(n) * time.Minute, nil
	case 'h':
		return time.Duration(n) * time.Hour, nil
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return 0, fmt.Errorf("unreachable")
}

// NextRun returns the next firing time for this schedule, given the most
// recent run (zero value if never run). Returns the zero time when the
// schedule has nothing left to fire (a one-shot that already ran or whose
// grace window has passed).
func (s Schedule) NextRun(lastRun time.Time, now time.Time) time.Time {
	switch s.Kind {
	case "once":
		if s.RunAt == nil {
			return time.Time{}
		}
		if !lastRun.IsZero() {
			return time.Time{}
		}
		// Still eligible if RunAt is in the future, or recently in the past
		// (within the grace window). Past that, treat as missed.
		if s.RunAt.Before(now.Add(-oneshotGracePeriod)) {
			return time.Time{}
		}
		return *s.RunAt
	case "interval":
		if s.Interval <= 0 {
			return time.Time{}
		}
		if lastRun.IsZero() {
			return now.Add(s.Interval)
		}
		return lastRun.Add(s.Interval)
	}
	return time.Time{}
}
