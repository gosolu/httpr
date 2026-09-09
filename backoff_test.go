package httpr

import (
	"net/http"
	"testing"
	"time"
)

func TestExponentialBackoff_NoJitter(t *testing.T) {
	b := &ExponentialBackoff{
		MinWait: 100 * time.Millisecond,
		MaxWait: 10 * time.Second,
		Factor:  2.0,
		Jitter:  NoJitter,
	}

	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 1, want: 100 * time.Millisecond},
		{attempt: 2, want: 200 * time.Millisecond},
		{attempt: 3, want: 400 * time.Millisecond},
		{attempt: 4, want: 800 * time.Millisecond},
		{attempt: 5, want: 1600 * time.Millisecond},
	}

	for _, tt := range tests {
		got := b.NextBackoff(tt.attempt, nil)
		if got != tt.want {
			t.Errorf("attempt %d: got %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

func TestExponentialBackoff_MaxWaitCap(t *testing.T) {
	b := &ExponentialBackoff{
		MinWait: 100 * time.Millisecond,
		MaxWait: 500 * time.Millisecond,
		Factor:  2.0,
		Jitter:  NoJitter,
	}

	got := b.NextBackoff(10, nil)
	if got != 500*time.Millisecond {
		t.Errorf("expected max wait cap 500ms, got %v", got)
	}
}

func TestExponentialBackoff_FullJitter(t *testing.T) {
	b := &ExponentialBackoff{
		MinWait: 100 * time.Millisecond,
		MaxWait: 2 * time.Second,
		Factor:  2.0,
		Jitter:  FullJitter,
	}

	for i := 0; i < 50; i++ {
		got := b.NextBackoff(2, nil)
		maxExpected := 200 * time.Millisecond
		if got < 0 || got > maxExpected {
			t.Errorf("attempt 2: got %v out of range [0, %v]", got, maxExpected)
		}
	}
}

func TestExponentialBackoff_EqualJitter(t *testing.T) {
	b := &ExponentialBackoff{
		MinWait: 100 * time.Millisecond,
		MaxWait: 2 * time.Second,
		Factor:  2.0,
		Jitter:  EqualJitter,
	}

	for i := 0; i < 50; i++ {
		got := b.NextBackoff(2, nil)
		minExpected := 100 * time.Millisecond // 200ms / 2 = 100ms
		maxExpected := 200 * time.Millisecond
		if got < minExpected || got > maxExpected {
			t.Errorf("attempt 2: got %v out of range [%v, %v]", got, minExpected, maxExpected)
		}
	}
}

func TestExponentialBackoff_RetryAfterHeader(t *testing.T) {
	b := NewExponentialBackoff()

	// Integer seconds
	resp := &http.Response{
		Header: http.Header{
			"Retry-After": []string{"5"},
		},
	}
	got := b.NextBackoff(1, resp)
	// Because MaxWait is 2s, 5s should be clamped to 2s
	if got != 2*time.Second {
		t.Errorf("expected 2s (clamped to maxWait), got %v", got)
	}

	// 1 second Retry-After should be respected within 2s max wait
	resp.Header.Set("Retry-After", "1")
	got = b.NextBackoff(1, resp)
	if got != 1*time.Second {
		t.Errorf("expected 1s, got %v", got)
	}
}

func TestLinearBackoff(t *testing.T) {
	lb := NewLinearBackoff(100*time.Millisecond, 50*time.Millisecond, 500*time.Millisecond)

	if got := lb.NextBackoff(1, nil); got != 100*time.Millisecond {
		t.Errorf("attempt 1: got %v, want 100ms", got)
	}
	if got := lb.NextBackoff(2, nil); got != 150*time.Millisecond {
		t.Errorf("attempt 2: got %v, want 150ms", got)
	}
	if got := lb.NextBackoff(3, nil); got != 200*time.Millisecond {
		t.Errorf("attempt 3: got %v, want 200ms", got)
	}
	if got := lb.NextBackoff(20, nil); got != 500*time.Millisecond {
		t.Errorf("attempt 20: got %v, want max 500ms", got)
	}
}

func TestConstantBackoff(t *testing.T) {
	cb := NewConstantBackoff(250 * time.Millisecond)

	for attempt := 1; attempt <= 5; attempt++ {
		if got := cb.NextBackoff(attempt, nil); got != 250*time.Millisecond {
			t.Errorf("attempt %d: got %v, want 250ms", attempt, got)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	// Nil response
	if _, ok := ParseRetryAfter(nil); ok {
		t.Errorf("expected false for nil response")
	}

	// Empty header
	resp := &http.Response{Header: make(http.Header)}
	if _, ok := ParseRetryAfter(resp); ok {
		t.Errorf("expected false for empty header")
	}

	// Seconds format
	resp.Header.Set("Retry-After", "120")
	dur, ok := ParseRetryAfter(resp)
	if !ok || dur != 120*time.Second {
		t.Errorf("got %v, %v, want 120s, true", dur, ok)
	}

	// Negative seconds
	resp.Header.Set("Retry-After", "-5")
	if _, ok := ParseRetryAfter(resp); ok {
		t.Errorf("expected false for negative seconds")
	}

	// HTTP Date format
	future := time.Now().Add(30 * time.Second).UTC()
	resp.Header.Set("Retry-After", future.Format(time.RFC1123))
	dur, ok = ParseRetryAfter(resp)
	if !ok || dur < 25*time.Second || dur > 35*time.Second {
		t.Errorf("expected approx 30s, got %v, %v", dur, ok)
	}

	// Invalid string
	resp.Header.Set("Retry-After", "invalid-value")
	if _, ok := ParseRetryAfter(resp); ok {
		t.Errorf("expected false for invalid string")
	}
}
