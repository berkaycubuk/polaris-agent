package agent

import (
	"testing"
)

func TestNewAgentDefaults(t *testing.T) {
	a := New(nil, nil, t.TempDir(), Options{})
	if a.maxToolIterations != 30 {
		t.Fatalf("expected default 30 iterations, got %d", a.maxToolIterations)
	}
}

func TestResetNonexistent(t *testing.T) {
	a := New(nil, nil, t.TempDir(), Options{})
	a.Reset("nope") // should not panic
}
