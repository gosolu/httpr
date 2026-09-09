package httpr

import (
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestTransport_RoundTripper(t *testing.T) {
	var attempts int32
	mock := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			return stringResponse(http.StatusServiceUnavailable, "unavailable"), nil
		}
		return stringResponse(http.StatusOK, "transport success"), nil
	})

	// Wrap in standard http.Client
	transport := NewRoundTripper(
		WithMaxRetries(3),
		WithTransport(mock),
		WithBackoff(NewConstantBackoff(time.Millisecond)),
	)
	httpClient := &http.Client{
		Transport: transport,
	}

	resp, err := httpClient.Get("http://example.com/transport")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "transport success" {
		t.Errorf("got %s, want transport success", string(body))
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestTransport_StandardClient(t *testing.T) {
	var attempts int32
	mock := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 2 {
			return stringResponse(http.StatusInternalServerError, "err"), nil
		}
		return stringResponse(http.StatusOK, "std client ok"), nil
	})

	client := NewClient(
		WithMaxRetries(2),
		WithTransport(mock),
		WithBackoff(NewConstantBackoff(time.Millisecond)),
	)
	stdClient := client.StandardClient()

	resp, err := stdClient.Get("http://example.com/std")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if atomic.LoadInt32(&attempts) != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
}
