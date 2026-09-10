package httpr

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/url"
)

// RetryPolicy determines whether an HTTP request should be retried based on the
// context, response, and error of the attempt.
// Returning true indicates that the request should be retried.
// If an error is returned, retries are halted immediately and that error is propagated.
type RetryPolicy func(ctx context.Context, resp *http.Response, err error) (bool, error)

// DefaultRetryPolicy is the standard policy used by httpr.
// It retries on:
//   - Network / connection errors, timeouts, connection refused/reset (unless overall context was canceled)
//   - Per-attempt timeouts (context.DeadlineExceeded when request context is still active)
//   - HTTP 408 (Request Timeout)
//   - HTTP 429 (Too Many Requests)
//   - HTTP 500 (Internal Server Error)
//   - HTTP 502 (Bad Gateway)
//   - HTTP 503 (Service Unavailable)
//   - HTTP 504 (Gateway Timeout)
//
// It specifically does NOT retry on:
//   - Request context cancellation or overall deadline exceeded
//   - HTTP 501 (Not Implemented)
//   - Standard 4xx client errors (400, 401, 403, 404, etc., except 408 and 429)
//   - TLS certificate verification errors
func DefaultRetryPolicy(ctx context.Context, resp *http.Response, err error) (bool, error) {
	// If the parent request context is canceled or timed out, never retry
	if ctx != nil && ctx.Err() != nil {
		return false, ctx.Err()
	}

	if err != nil {
		// Do not retry if circuit breaker rejected the request
		if errors.Is(err, ErrCircuitOpen) {
			return false, err
		}
		if errors.Is(err, context.Canceled) {
			return false, nil
		}
		// If attempt timed out but overall context is still active, retry
		if errors.Is(err, context.DeadlineExceeded) {
			return true, nil
		}
		return IsTransientError(err), nil
	}

	if resp != nil {
		return IsTransientStatusCode(resp.StatusCode), nil
	}

	return false, nil
}

// IsTransientStatusCode returns true if the status code indicates a temporary server error
// or rate limiting condition suitable for retry.
func IsTransientStatusCode(code int) bool {
	switch code {
	case http.StatusRequestTimeout, // 408
		http.StatusTooManyRequests,     // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout:      // 504
		return true
	default:
		return false
	}
}

// IsTransientError inspects an error to decide if it is a transient network or connection error.
func IsTransientError(err error) bool {
	if err == nil {
		return false
	}

	// Never retry if the error is due to explicit cancellation
	if errors.Is(err, context.Canceled) {
		return false
	}

	// Never retry if circuit breaker is open
	if errors.Is(err, ErrCircuitOpen) {
		return false
	}

	// Unwrap *url.Error if present
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		// URL parse errors or bad redirects shouldn't be retried
		if urlErr.Op == "parse" {
			return false
		}
		// Do not retry TLS certificate verification failures
		var certErr x509.CertificateInvalidError
		var unknownAuthErr x509.UnknownAuthorityError
		var hostErr x509.HostnameError
		if errors.As(urlErr.Err, &certErr) ||
			errors.As(urlErr.Err, &unknownAuthErr) ||
			errors.As(urlErr.Err, &hostErr) {
			return false
		}
		if urlErr.Timeout() {
			return true
		}
		err = urlErr.Err
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	// Check net.OpError and other network conditions
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	// Connection refused, reset, broken pipe, etc.
	return true
}

// RetryOnStatusCodes creates a RetryPolicy that retries if the response status code is in the list.
func RetryOnStatusCodes(statusCodes ...int) RetryPolicy {
	codeSet := make(map[int]struct{}, len(statusCodes))
	for _, sc := range statusCodes {
		codeSet[sc] = struct{}{}
	}

	return func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		if ctx != nil && ctx.Err() != nil {
			return false, ctx.Err()
		}
		if resp != nil {
			if _, ok := codeSet[resp.StatusCode]; ok {
				return true, nil
			}
		}
		return false, nil
	}
}

// RetryOnNetworkErrors creates a RetryPolicy that retries on any transient network error.
func RetryOnNetworkErrors() RetryPolicy {
	return func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		if ctx != nil && ctx.Err() != nil {
			return false, ctx.Err()
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return false, nil
			}
			return IsTransientError(err), nil
		}
		return false, nil
	}
}

// CombinePolicies combines multiple RetryPolicies using OR logic.
// If any policy votes to retry, it will be retried (unless one returns an error).
func CombinePolicies(policies ...RetryPolicy) RetryPolicy {
	return func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		for _, policy := range policies {
			if policy == nil {
				continue
			}
			retry, pErr := policy(ctx, resp, err)
			if pErr != nil {
				return false, pErr
			}
			if retry {
				return true, nil
			}
		}
		return false, nil
	}
}

// NeverRetry returns a RetryPolicy that never retries any request.
func NeverRetry() RetryPolicy {
	return func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		return false, nil
	}
}
