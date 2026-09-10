package httpr

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RetryError wraps the last error encountered when all retry attempts have been exhausted.
// It implements the unwrap interface so errors.Is and errors.As continue to work.
type RetryError struct {
	Attempts   int
	MaxRetries int
	LastErr    error
	Response   *http.Response
}

func (e *RetryError) Error() string {
	if e.LastErr != nil {
		return fmt.Sprintf("httpr: request failed after %d attempts (max %d retries): %v", e.Attempts, e.MaxRetries, e.LastErr)
	}
	if e.Response != nil {
		return fmt.Sprintf("httpr: request failed after %d attempts with HTTP status %d", e.Attempts, e.Response.StatusCode)
	}
	return fmt.Sprintf("httpr: request failed after %d attempts", e.Attempts)
}

func (e *RetryError) Unwrap() error {
	return e.LastErr
}

// Client wraps HTTP operations with customizable, automated retries.
type Client struct {
	opts Options
}

// NewClient creates a new httpr Client configured with functional options.
// If no options are provided, production-ready defaults are used.
func NewClient(opts ...Option) *Client {
	options := DefaultOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(options)
		}
	}

	// If circuit breaker is enabled, automatically wrap the internal transport
	if options.CircuitBreakerEnabled && options.CircuitBreaker != nil {
		if options.HTTPClient == nil {
			options.HTTPClient = &http.Client{}
		}
		baseTransport := options.HTTPClient.Transport
		if baseTransport == nil {
			baseTransport = defaultTransport()
		}
		options.HTTPClient.Transport = NewCircuitBreakerTransport(
			baseTransport,
			options.CircuitBreaker,
			options.CircuitBreakerClassifier,
		)
	}

	return &Client{
		opts: *options,
	}
}

// Options returns a copy of the client's current options.
func (c *Client) Options() Options {
	return c.opts
}

// Do executes an HTTP request with retry logic, using the provided context instead of the Request's existing context.
func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("httpr: request cannot be nil")
	}

	if ctx == nil {
		ctx = req.Context()
		if ctx == nil {
			ctx = context.Background()
		}
	}

	// Associate the provided context with the request
	req = req.WithContext(ctx)

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Prepare request body for rewindability
	if err := prepareRequest(req, c.opts.MaxBodyBytes); err != nil {
		return nil, err
	}

	maxRetries := c.opts.MaxRetries
	attempt := 0

	for {
		attempt++

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Rewind request body for retry attempts
		if attempt > 1 {
			if err := rewindBody(req); err != nil {
				return nil, err
			}
		}

		// Configure per-attempt context if requested
		attemptCtx := ctx
		var cancel context.CancelFunc
		if c.opts.PerAttemptTimeout > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, c.opts.PerAttemptTimeout)
		}

		attemptReq := req.Clone(attemptCtx)

		// Execute HTTP attempt
		resp, err := c.opts.HTTPClient.Do(attemptReq)
		if cancel != nil {
			cancel()
		}

		if c.opts.AfterAttempt != nil {
			c.opts.AfterAttempt(attempt, attemptReq, resp, err)
		}

		// Check retry policy
		shouldRetry, policyErr := c.opts.RetryPolicy(ctx, resp, err)
		if policyErr != nil {
			// Fatal error from policy halts immediately
			return resp, policyErr
		}

		// If policy says not to retry, we're done
		if !shouldRetry {
			return resp, err
		}

		// If we've exhausted all retry attempts
		if attempt > maxRetries {
			if err != nil {
				return resp, &RetryError{
					Attempts:   attempt,
					MaxRetries: maxRetries,
					LastErr:    err,
					Response:   resp,
				}
			}
			// If response is present without error (e.g. 500 status code), return response as-is
			return resp, nil
		}

		// Safe cleanup of response body before retrying
		drainAndClose(resp)

		// Calculate backoff wait duration
		wait := c.opts.Backoff.NextBackoff(attempt, resp)

		if c.opts.OnRetry != nil {
			c.opts.OnRetry(attempt, req, resp, err, wait)
		}

		// Sleep or abort if context is canceled
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

// Get issues a GET request to the specified URL using the provided context with retry support.
func (c *Client) Get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(ctx, req)
}

// Head issues a HEAD request to the specified URL using the provided context with retry support.
func (c *Client) Head(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(ctx, req)
}

// Post issues a POST request with the specified body to the target URL using the provided context with retry support.
func (c *Client) Post(ctx context.Context, url, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.Do(ctx, req)
}

// PostForm issues a POST with form urlencoded data using the provided context with retry support.
func (c *Client) PostForm(ctx context.Context, targetURL string, data url.Values) (*http.Response, error) {
	return c.Post(ctx, targetURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
}

// StandardClient returns a standard library *http.Client configured with this httpr Client's
// retry policies and settings via an http.RoundTripper transport.
func (c *Client) StandardClient() *http.Client {
	return &http.Client{
		Transport: NewRoundTripperWithClient(c),
	}
}
