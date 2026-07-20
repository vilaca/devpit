package engine

import (
	"testing"
	"time"
)

func TestBackoffReadyAndReset(t *testing.T) {
	var b backoff
	if !b.ready() {
		t.Fatal("zero-value backoff should be ready")
	}

	b.bump()
	if b.ready() {
		t.Fatal("backoff should be closed immediately after bump")
	}
	if b.streak != 1 {
		t.Fatalf("streak = %d, want 1", b.streak)
	}

	b.reset()
	if !b.ready() {
		t.Fatal("backoff should be ready after reset")
	}
	if b.streak != 0 {
		t.Fatalf("streak after reset = %d, want 0", b.streak)
	}
}

// TestBackoffDelayProgression pins the exponential schedule 1→2→4→8→16 min,
// capped at 16 (§8).
func TestBackoffDelayProgression(t *testing.T) {
	want := []time.Duration{
		1 * time.Minute,
		2 * time.Minute,
		4 * time.Minute,
		8 * time.Minute,
		16 * time.Minute,
		16 * time.Minute, // capped
		16 * time.Minute,
	}
	var b backoff
	for i, w := range want {
		b.streak = i + 1
		if got := b.delay(); got != w {
			t.Errorf("streak %d: delay = %s, want %s", b.streak, got, w)
		}
	}
}

func TestBackoffRateLimitFloor(t *testing.T) {
	sec := func(n int) *time.Duration { d := time.Duration(n) * time.Second; return &d }

	tests := []struct {
		name string
		hint *time.Duration
		want time.Duration // streak starts at 0, so exponential component is 1 min
	}{
		{"no hint uses exponential", nil, time.Minute},
		{"hint below floor uses exponential", sec(30), time.Minute},
		{"hint above floor wins", sec(90), 90 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b backoff
			got := b.rateLimit(tt.hint)
			if got != tt.want {
				t.Errorf("rateLimit = %s, want %s", got, tt.want)
			}
			if b.streak != 1 {
				t.Errorf("streak = %d, want 1", b.streak)
			}
			if b.ready() {
				t.Error("backoff should be closed after rateLimit")
			}
		})
	}
}

// TestBackoffRateLimitEscalates verifies that once the exponential streak
// overtakes the hint, the longer wait wins (delay = max(exponential, hint)).
func TestBackoffRateLimitEscalates(t *testing.T) {
	var b backoff
	b.streak = 5 // next exponential component is 16 min
	hint := 2 * time.Minute
	if got := b.rateLimit(&hint); got != 16*time.Minute {
		t.Fatalf("rateLimit = %s, want 16m (exponential beats the smaller hint)", got)
	}
}
