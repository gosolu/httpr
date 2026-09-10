package httpr_test

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gosolu/httpr"
)

func ExampleNewClient() {
	// Create a client with default settings:
	// - 3 retries
	// - Exponential backoff with full jitter
	// - Automatically retries on 5xx, 429, and network errors
	client := httpr.NewClient()

	_ = client
	fmt.Println("Client initialized")
	// Output: Client initialized
}

func ExampleClient_Get() {
	client := httpr.NewClient()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Get accepts a context.Context explicitly
	_ = ctx
	_ = client
	fmt.Println("Context-aware Get request")
	// Output: Context-aware Get request
}

func ExampleWithExponentialBackoff() {
	client := httpr.NewClient(
		httpr.WithMaxRetries(5),
		httpr.WithExponentialBackoff(
			50*time.Millisecond, // min wait
			5*time.Second,       // max wait
			2.0,                 // exponential multiplier factor
			httpr.FullJitter,    // jitter mode
		),
	)

	_ = client
	fmt.Println("Exponential backoff configured")
	// Output: Exponential backoff configured
}

func ExampleWithRetryPolicy() {
	// Custom retry policy: only retry 503 Service Unavailable and 429 Too Many Requests
	customPolicy := func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		if err != nil {
			return true, nil // retry transient network errors
		}
		if resp != nil && (resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusTooManyRequests) {
			return true, nil
		}
		return false, nil
	}

	client := httpr.NewClient(
		httpr.WithRetryPolicy(customPolicy),
		httpr.WithMaxRetries(2),
	)

	_ = client
	fmt.Println("Custom retry policy configured")
	// Output: Custom retry policy configured
}

func ExampleNewRoundTripper() {
	// Inject retry logic into any standard *http.Client
	httpClient := &http.Client{
		Transport: httpr.NewRoundTripper(
			httpr.WithMaxRetries(3),
		),
	}

	_ = httpClient
	fmt.Println("RoundTripper configured")
	// Output: RoundTripper configured
}

func ExampleWithCircuitBreaker() {
	// Enable built-in SRE circuit breaker with adaptive throttling
	client := httpr.NewClient(
		httpr.WithMaxRetries(3),
		httpr.WithCircuitBreaker(
			httpr.WithSuccessRatio(0.6), // K = 1 / 0.6 ≈ 1.67
			httpr.WithMinRequests(20),  // activate after 20 requests in window
			httpr.WithWindow(5*time.Second),
		),
	)

	_ = client
	fmt.Println("Circuit breaker configured")
	// Output: Circuit breaker configured
}
