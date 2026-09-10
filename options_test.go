package httpr

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOptions_Config(t *testing.T) {
	stdClient := &http.Client{}

	opts := DefaultOptions()
	c := NewClient(
		WithMaxRetries(-1), // should clamp to 0
		WithHTTPClient(stdClient),
		WithMaxBodyBytes(4096),
		WithCheckRetry(func(ctx context.Context, resp *http.Response, err error) (bool, error) {
			return false, nil
		}),
	)

	cfg := c.Options()
	if cfg.MaxRetries != 0 {
		t.Errorf("expected MaxRetries clamped to 0, got %d", cfg.MaxRetries)
	}
	if cfg.MaxBodyBytes != 4096 {
		t.Errorf("expected MaxBodyBytes 4096, got %d", cfg.MaxBodyBytes)
	}
	if cfg.HTTPClient != stdClient {
		t.Errorf("expected HTTPClient to match provided client")
	}

	// Test WithLinearBackoff and WithRetryAfterHeader
	c2 := NewClient(
		WithLinearBackoff(50*time.Millisecond, 10*time.Millisecond, 200*time.Millisecond, true),
		WithRetryAfterHeader(false),
	)
	if lb, ok := c2.Options().Backoff.(*LinearBackoff); !ok || lb.CheckRetryAfter {
		t.Errorf("expected LinearBackoff with CheckRetryAfter=false")
	}

	// Test WithConstantBackoff and WithRetryAfterHeader
	c3 := NewClient(
		WithConstantBackoff(100*time.Millisecond, true),
		WithRetryAfterHeader(false),
	)
	if cb, ok := c3.Options().Backoff.(*ConstantBackoff); !ok || cb.CheckRetryAfter {
		t.Errorf("expected ConstantBackoff with CheckRetryAfter=false")
	}

	// Test WithRetryStatusCodes
	c4 := NewClient(WithRetryStatusCodes(http.StatusTeapot))
	retry, _ := c4.Options().RetryPolicy(context.Background(), &http.Response{StatusCode: http.StatusTeapot}, nil)
	if !retry {
		t.Errorf("expected WithRetryStatusCodes to retry 418")
	}

	// Test RetryOnNetworkErrors
	netPolicy := RetryOnNetworkErrors()
	retry, _ = netPolicy(context.Background(), nil, errors.New("any network error"))
	if !retry {
		t.Errorf("expected RetryOnNetworkErrors to retry network error")
	}

	_ = opts
}

func TestRetryError_Error(t *testing.T) {
	err1 := &RetryError{
		Attempts:   3,
		MaxRetries: 2,
		LastErr:    errors.New("dial error"),
	}
	if !strings.Contains(err1.Error(), "dial error") {
		t.Errorf("unexpected error string: %s", err1.Error())
	}

	err2 := &RetryError{
		Attempts: 4,
		Response: &http.Response{StatusCode: 503},
	}
	if !strings.Contains(err2.Error(), "HTTP status 503") {
		t.Errorf("unexpected error string: %s", err2.Error())
	}

	err3 := &RetryError{Attempts: 2}
	if !strings.Contains(err3.Error(), "after 2 attempts") {
		t.Errorf("unexpected error string: %s", err3.Error())
	}
}

func TestBackoff_EdgeCases(t *testing.T) {
	// ExponentialBackoff edge cases: minWait <= 0, maxWait <= 0, factor <= 1, attempt < 1
	eb := &ExponentialBackoff{
		MinWait: -1,
		MaxWait: -1,
		Factor:  0.5,
		Jitter:  NoJitter,
	}
	dur := eb.NextBackoff(0, nil)
	if dur != 100*time.Millisecond {
		t.Errorf("expected default minWait 100ms, got %v", dur)
	}

	// LinearBackoff with Retry-After header
	lb := NewLinearBackoff(50*time.Millisecond, 10*time.Millisecond, 200*time.Millisecond)
	resp := &http.Response{Header: http.Header{"Retry-After": []string{"1"}}}
	dur = lb.NextBackoff(1, resp)
	if dur != 200*time.Millisecond { // 1s clamped to maxWait 200ms
		t.Errorf("expected 200ms, got %v", dur)
	}

	// ConstantBackoff with Retry-After header
	cb := NewConstantBackoff(100 * time.Millisecond)
	dur = cb.NextBackoff(1, resp)
	if dur != 1*time.Second {
		t.Errorf("expected 1s, got %v", dur)
	}

	// NewRoundTripperWithClient nil client
	rt := NewRoundTripperWithClient(nil)
	if rt == nil {
		t.Errorf("expected non-nil round tripper")
	}
}

func TestInvalidURLErrorPaths(t *testing.T) {
	c := NewClient()

	// Invalid URLs containing control characters will fail http.NewRequest
	badURL := "http://example.com/\x7f"
	if _, err := c.Get(context.Background(), badURL); err == nil {
		t.Errorf("expected error for bad GET URL")
	}
	if _, err := c.Head(context.Background(), badURL); err == nil {
		t.Errorf("expected error for bad HEAD URL")
	}
	if _, err := c.Post(context.Background(), badURL, "text/plain", nil); err == nil {
		t.Errorf("expected error for bad POST URL")
	}

	// Exponential backoff WithRetryAfterHeader(false)
	cExp := NewClient(WithRetryAfterHeader(false))
	if eb, ok := cExp.Options().Backoff.(*ExponentialBackoff); !ok || eb.CheckRetryAfter {
		t.Errorf("expected ExponentialBackoff with CheckRetryAfter=false")
	}

	// RetryOnNetworkErrors with canceled context
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	netPolicy := RetryOnNetworkErrors()
	if retry, _ := netPolicy(canceledCtx, nil, errors.New("net err")); retry {
		t.Errorf("expected no retry when context is canceled")
	}

	// CombinePolicies with error
	fatalErr := errors.New("fatal")
	pFatal := func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		return false, fatalErr
	}
	pCombined := CombinePolicies(nil, pFatal)
	if _, err := pCombined(context.Background(), nil, nil); !errors.Is(err, fatalErr) {
		t.Errorf("expected fatal error from CombinePolicies")
	}
}
