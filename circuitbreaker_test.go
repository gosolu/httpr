package httpr

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestSREBreaker_InitialAllowed(t *testing.T) {
	b := NewSREBreaker(
		WithMinRequests(10),
		WithSuccessRatio(0.5),
	)

	// Requests under minRequests must always be allowed
	for i := 0; i < 9; i++ {
		if err := b.Allow(); err != nil {
			t.Fatalf("expected request %d to be allowed, got %v", i+1, err)
		}
		b.MarkFailed() // even if failed, minRequests threshold not reached yet
	}
}

func TestSREBreaker_ThrottlingWhenFailuresAccumulate(t *testing.T) {
	b := NewSREBreaker(
		WithMinRequests(10),
		WithSuccessRatio(0.5), // K = 2.0
		WithWindow(10*time.Second),
		WithBuckets(10),
	)

	// Simulate 100 consecutive failures
	for i := 0; i < 100; i++ {
		b.MarkFailed()
	}

	// Now drop probability should be near (100 - 2*0)/(101) ≈ 99%
	var rejected int
	for i := 0; i < 100; i++ {
		if err := b.Allow(); errors.Is(err, ErrCircuitOpen) {
			rejected++
		}
	}

	if rejected < 70 {
		t.Errorf("expected heavy rejection (>70%%), got %d/100 rejected", rejected)
	}
}

func TestSREBreaker_RecoveryOnSuccess(t *testing.T) {
	b := NewSREBreaker(
		WithMinRequests(10),
		WithSuccessRatio(0.5),
		WithWindow(10*time.Second),
	)

	// Accumulate 50 failures
	for i := 0; i < 50; i++ {
		b.MarkFailed()
	}

	// Now report 200 successes (accepted = 2.0 * 200 = 400, total = 250 < 400)
	for i := 0; i < 200; i++ {
		b.MarkSuccess()
	}

	// Now it should be completely healthy (total < K*successes)
	for i := 0; i < 20; i++ {
		if err := b.Allow(); err != nil {
			t.Errorf("expected request to be allowed after recovery, got %v", err)
		}
	}
}

func TestClient_WithCircuitBreaker_WrapTransport(t *testing.T) {
	var attempts int32
	mock := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&attempts, 1)
		return stringResponse(http.StatusInternalServerError, "error"), nil
	})

	client := NewClient(
		WithMaxRetries(3),
		WithTransport(mock),
		WithBackoff(NewConstantBackoff(time.Millisecond)),
		WithCircuitBreaker(
			WithMinRequests(5),
			WithSuccessRatio(0.5),
			WithWindow(5*time.Second),
		),
	)

	// First request will attempt and retry
	ctx := context.Background()
	_, _ = client.Get(ctx, "http://example.com/cb-test")

	// Verify that transport was wrapped
	cbTransport, ok := client.Options().HTTPClient.Transport.(*CircuitBreakerTransport)
	if !ok {
		t.Fatalf("expected transport to be *CircuitBreakerTransport, got %T", client.Options().HTTPClient.Transport)
	}
	if cbTransport.breaker == nil {
		t.Errorf("expected breaker to be non-nil")
	}
}

type mockBreaker struct {
	allowErr     error
	allowCount   int32
	successCount int32
	failedCount  int32
}

func (m *mockBreaker) Allow() error {
	atomic.AddInt32(&m.allowCount, 1)
	return m.allowErr
}

func (m *mockBreaker) MarkSuccess() {
	atomic.AddInt32(&m.successCount, 1)
}

func (m *mockBreaker) MarkFailed() {
	atomic.AddInt32(&m.failedCount, 1)
}

func TestClient_CircuitBreaker_FailFastNoRetries(t *testing.T) {
	mock := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusOK, "ok"), nil
	})

	cb := &mockBreaker{
		allowErr: ErrCircuitOpen,
	}

	client := NewClient(
		WithMaxRetries(5),
		WithTransport(mock),
		WithCustomCircuitBreaker(cb),
	)

	_, err := client.Get(context.Background(), "http://example.com/fail-fast")
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}

	// Must fail fast without retrying! Allow count should be 1
	if count := atomic.LoadInt32(&cb.allowCount); count != 1 {
		t.Errorf("expected exactly 1 attempt (no retries against open circuit), got %d", count)
	}
}

func TestCircuitBreaker_CustomClassifier(t *testing.T) {
	cb := &mockBreaker{}
	mock := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusTooManyRequests, "rate limited"), nil
	})

	// Custom classifier that treats 429 as failure
	classifier := func(resp *http.Response, err error) bool {
		if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
			return true
		}
		return DefaultCircuitBreakerClassifier(resp, err)
	}

	transport := NewCircuitBreakerTransport(mock, cb, classifier)
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	_, _ = transport.RoundTrip(req)

	if atomic.LoadInt32(&cb.failedCount) != 1 {
		t.Errorf("expected 429 to be classified as failure, failedCount=%d", cb.failedCount)
	}
}

func TestCircuitBreaker_OptionsEdgeCases(t *testing.T) {
	cfg := DefaultCircuitBreakerConfig()
	if cfg.SuccessRatio != 0.6 || cfg.MinRequests != 20 || cfg.Buckets != 10 {
		t.Errorf("unexpected default config: %+v", cfg)
	}

	WithSuccessRatio(-1)(cfg)
	if cfg.SuccessRatio != 0.6 {
		t.Errorf("expected invalid success ratio ignored")
	}

	WithMinRequests(-5)(cfg)
	if cfg.MinRequests != 20 {
		t.Errorf("expected invalid minRequests ignored")
	}

	WithWindow(-time.Second)(cfg)
	if cfg.Window != 5*time.Second {
		t.Errorf("expected invalid window ignored")
	}

	WithBuckets(-1)(cfg)
	if cfg.Buckets != 10 {
		t.Errorf("expected invalid buckets ignored")
	}

	WithFailureClassifier(nil)(cfg)
	if cfg.Classifier == nil {
		t.Errorf("expected classifier to stay non-nil")
	}

	// Test window advance logic
	w := newRollingWindow(3, 10*time.Millisecond)
	w.Add(1, 1)
	time.Sleep(25 * time.Millisecond)
	w.Add(1, 1)
	s, tot := w.Summary()
	if tot != 2 || s != 2 {
		t.Errorf("expected 2 total, 2 successes, got %d, %d", tot, s)
	}

	// Test NewCircuitBreakerTransport nil base
	cbt := NewCircuitBreakerTransport(nil, nil, nil)
	if cbt == nil {
		t.Fatalf("expected non-nil transport")
	}
}

func TestDefaultCircuitBreakerClassifier(t *testing.T) {
	// ErrCircuitOpen should not be counted as a failure
	if DefaultCircuitBreakerClassifier(nil, ErrCircuitOpen) {
		t.Errorf("ErrCircuitOpen should not be counted as failure")
	}

	// Normal error is a failure
	if !DefaultCircuitBreakerClassifier(nil, errors.New("conn reset")) {
		t.Errorf("network error should be counted as failure")
	}

	// 500 is a failure
	if !DefaultCircuitBreakerClassifier(&http.Response{StatusCode: 500}, nil) {
		t.Errorf("500 should be counted as failure")
	}

	// 200 is success
	if DefaultCircuitBreakerClassifier(&http.Response{StatusCode: 200}, nil) {
		t.Errorf("200 should not be counted as failure")
	}

	// 404 is success (server responded properly)
	if DefaultCircuitBreakerClassifier(&http.Response{StatusCode: 404}, nil) {
		t.Errorf("404 should not be counted as failure")
	}

	// Neither response nor error
	if DefaultCircuitBreakerClassifier(nil, nil) {
		t.Errorf("nil response and nil error should not be failure")
	}

	// Test WithCircuitBreakerClassifier option
	client := NewClient(
		WithCircuitBreaker(),
		WithCircuitBreakerClassifier(func(resp *http.Response, err error) bool { return false }),
	)
	if client.Options().CircuitBreakerClassifier == nil {
		t.Errorf("expected classifier to be set")
	}
}
