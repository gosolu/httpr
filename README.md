# httpr

A production-grade, zero-dependency Go HTTP client library with customizable automated retries, exponential backoff with jitter, rewindable request bodies, and seamless `http.RoundTripper` integration.

[![Go Reference](https://pkg.go.dev/badge/github.com/gosolu/httpr.svg)](https://pkg.go.dev/github.com/gosolu/httpr)
[![Go Report Card](https://goreportcard.com/badge/github.com/gosolu/httpr)](https://goreportcard.com/report/github.com/gosolu/httpr)

---

## Features

- **Zero External Dependencies**: Pure standard library Go (Go 1.22+).
- **All Features Are Optional**: Sensible production-ready defaults out of the box (`httpr.NewClient()`).
- **Global Default Methods**: Package-level `httpr.Get`, `httpr.Post`, `httpr.Put`, `httpr.Delete`, `httpr.Head`, `httpr.PostForm`, and `httpr.Do` for zero-boilerplate requests using a thread-safe, lazily-initialized default client (`httpr.DefaultClient()`).
- **Context-First Design**: All methods (`Do`, `Get`, `Post`, `Put`, `Delete`, `Head`, `PostForm`) take a `context.Context` parameter, prioritizing it over any context attached to the request.
- **Flexible Backoff Strategies**:
  - Exponential Backoff (configurable factor, min/max wait caps)
  - Jitter support: `FullJitter`, `EqualJitter`, and `NoJitter` (mitigates the *thundering herd* problem)
  - Linear and Constant backoff strategies
  - Automatic `Retry-After` header parsing (both seconds and HTTP RFC1123 dates)
- **Built-in SRE Circuit Breaker**:
  - Google SRE client-side adaptive throttling algorithm (adapted from `go-kratos/aegis/circuitbreaker`).
  - Zero external dependencies.
  - Automatically wraps the client's internal transport via `httpr.WithCircuitBreaker(...)`.
  - Automatically halts retries when the breaker throttles requests (`ErrCircuitOpen`).
  - Configurable success ratios, minimum request thresholds, rolling window durations, and custom failure classifiers.
- **Extensible Retry Policies**:
  - Default policy: retries transient network errors, timeouts, connection refused/reset, and HTTP 408, 429, 500, 502, 503, 504.
  - Safe: avoids retrying non-idempotent 4xx client errors (400, 401, 403, 404, etc.) or 501 Not Implemented.
  - Custom predicate support: customize via `WithRetryPolicy` or inline `WithCheckRetry`.
- **Safe Request Body Rewinding**:
  - Automatically handles POST/PUT payloads (`req.GetBody` or memory-buffered rewind up to a configurable size limit).
  - Automatically drains and closes discarded response bodies to ensure connection reuse without leaking sockets.
- **Context & Timeout Control**:
  - Immediate cancellation when overall `context.Context` is canceled.
  - Optional `WithPerAttemptTimeout` for individual attempt timeouts without overriding the overall context deadline.
- **Seamless Standard Library Integration**:
  - Provides `http.RoundTripper` (`httpr.NewRoundTripper`) to add retries directly into any existing `*http.Client` (e.g. AWS SDK, Google Cloud SDK, OpenAPI clients).
  - Convenience methods: `Get`, `Head`, `Post`, `Put`, `Delete`, `PostForm`, and `StandardClient()`.
- **Observability**:
  - `WithOnRetry` and `WithAfterAttempt` lifecycle hooks for custom logging, metrics, and tracing.

---

## Installation

```bash
go get github.com/gosolu/httpr
```

---

## Quick Start

### Global Default Methods (Zero-Setup)

For quick scripts or standard microservices, use package-level functions directly without initializing a client:

```go
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/gosolu/httpr"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Direct call using the global default client (lazily initialized)
	resp, err := httpr.Get(ctx, "https://api.example.com/data")
	if err != nil {
		log.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("Response: %s\n", string(body))
}
```

You can also customize the global default client globally:

```go
// Replace the global default client with customized options
httpr.SetDefaultClient(httpr.NewClient(
	httpr.WithMaxRetries(5),
	httpr.WithCircuitBreaker(),
))
```

### Explicit Client Usage with Defaults

```go
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/gosolu/httpr"
)

func main() {
	// Create client with default options:
	// - 3 retries (up to 4 total attempts)
	// - Exponential backoff with full jitter (100ms - 2s)
	// - Retries 5xx, 429, 408, and network errors
	client := httpr.NewClient()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.Get(ctx, "https://api.example.com/data")
	if err != nil {
		log.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("Response (%d): %s\n", resp.StatusCode, string(body))
}
```

---

## Detailed Configuration

All options are optional and configured via the functional options pattern:

```go
client := httpr.NewClient(
    // Retries
    httpr.WithMaxRetries(5),

    // Exponential Backoff with Jitter
    httpr.WithExponentialBackoff(
        50*time.Millisecond, // initial wait
        3*time.Second,       // max wait cap
        2.0,                 // exponential factor
        httpr.FullJitter,    // jitter algorithm
    ),

    // Per-attempt timeout
    httpr.WithPerAttemptTimeout(2 * time.Second),

    // Custom HTTP Transport or Client
    httpr.WithTransport(&http.Transport{
        MaxIdleConns: 50,
    }),

    // Lifecycle hooks for logging, metrics, and tracing
    httpr.WithOnRetry(func(attempt int, req *http.Request, resp *http.Response, err error, wait time.Duration) {
        log.Printf("Attempt %d failed (err: %v); retrying in %v...", attempt, err, wait)
    }),
)
```

---

## Backoff Strategies

### 1. Exponential Backoff (Default)

Calculates wait time exponentially: `minWait * factor^(attempt-1)` capped at `maxWait`:

```go
client := httpr.NewClient(
    httpr.WithExponentialBackoff(100*time.Millisecond, 2*time.Second, 2.0, httpr.FullJitter),
)
```

Available jitter strategies:
- `httpr.FullJitter` (recommended): Uniform random duration between `0` and `wait`.
- `httpr.EqualJitter`: `wait/2 + rand(0, wait/2)`.
- `httpr.NoJitter`: Deterministic backoff without randomization.

### 2. Linear Backoff

Linearly increases delay with each retry attempt: `minWait + (attempt-1) * step`:

```go
client := httpr.NewClient(
    httpr.WithLinearBackoff(100*time.Millisecond, 50*time.Millisecond, 1*time.Second, true),
)
```

### 3. Constant Backoff

Pauses for a fixed interval between retries:

```go
client := httpr.NewClient(
    httpr.WithConstantBackoff(250*time.Millisecond, true),
)
```

### 4. `Retry-After` Header

By default, all backoff strategies inspect the `Retry-After` header returned by rate-limited servers (`429 Too Many Requests` or `503 Service Unavailable`). It supports both integer seconds (`Retry-After: 30`) and HTTP dates (`Retry-After: Fri, 31 Dec 2026 23:59:59 GMT`).

To disable respecting `Retry-After`:
```go
httpr.WithRetryAfterHeader(false)
```

---

## Custom Retry Policies

You can inspect the `context.Context`, `*http.Response`, and `error` to decide if an attempt should be retried:

```go
client := httpr.NewClient(
    httpr.WithRetryPolicy(func(ctx context.Context, resp *http.Response, err error) (bool, error) {
        // Halt if context is canceled
        if ctx.Err() != nil {
            return false, ctx.Err()
        }
        // Fatal error: halt retries immediately
        if errors.Is(err, ErrUnrecoverable) {
            return false, err
        }
        // Retry specific status codes
        if resp != nil && resp.StatusCode == http.StatusServiceUnavailable {
            return true, nil
        }
        // Retry transient network issues
        return httpr.IsTransientError(err), nil
    }),
)
```

### Convenience Policy Builders

- `httpr.WithRetryStatusCodes(http.StatusBadGateway, http.StatusServiceUnavailable)`
- `httpr.RetryOnStatusCodes(...)`
- `httpr.RetryOnNetworkErrors()`
- `httpr.CombinePolicies(p1, p2)`: Combines policies with OR logic.
- `httpr.NeverRetry()`

---

## Circuit Breaker (Google SRE Adaptive Throttling)

`httpr` includes a built-in, zero-dependency circuit breaker based on the **Google SRE client-side adaptive throttling** algorithm (adapted from [`go-kratos/aegis/circuitbreaker`](https://github.com/go-kratos/aegis/tree/main/circuitbreaker)).

When enabled, `httpr` automatically wraps the client's internal transport with the circuit breaker. If failures exceed acceptable thresholds, the breaker begins dropping requests with `httpr.ErrCircuitOpen`. The retry engine detects this and **halts immediately**, preventing retry storms.

```go
client := httpr.NewClient(
    httpr.WithMaxRetries(3),
    // Enable the built-in SRE circuit breaker
    httpr.WithCircuitBreaker(
        httpr.WithSuccessRatio(0.6),      // K = 1 / 0.6 ≈ 1.67 (default 0.6)
        httpr.WithMinRequests(20),       // Threshold before throttling begins (default 20)
        httpr.WithWindow(5*time.Second), // Rolling window size (default 5s)
        httpr.WithBuckets(10),           // Number of sliding buckets (default 10)
    ),
)

// When circuit is open, requests fail fast with httpr.ErrCircuitOpen:
resp, err := client.Get(ctx, "https://api.example.com/endpoint")
if errors.Is(err, httpr.ErrCircuitOpen) {
    // Fast-fail handling without waiting for timeouts or retries
}
```

### Custom Failure Classifier

By default, network errors and HTTP `5xx` responses are counted as failures, while `2xx`, `3xx`, and `4xx` responses are counted as successes. You can customize this behavior:

```go
client := httpr.NewClient(
    httpr.WithCircuitBreaker(
        httpr.WithFailureClassifier(func(resp *http.Response, err error) bool {
            // Also treat 429 Too Many Requests as circuit breaker failures
            if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
                return true
            }
            return httpr.DefaultCircuitBreakerClassifier(resp, err)
        }),
    ),
)
```

---

## Integrating with Existing SDKs (`RoundTripper`)

Any third-party SDK or standard `*http.Client` can use `httpr` by setting its `Transport`:

```go
// Create a standard http.Client with automatic retries
httpClient := &http.Client{
    Timeout: 10 * time.Second,
    Transport: httpr.NewRoundTripper(
        httpr.WithMaxRetries(3),
        httpr.WithExponentialBackoff(50*time.Millisecond, 1*time.Second, 2.0, httpr.FullJitter),
    ),
}

// Or obtain one directly from an httpr.Client:
client := httpr.NewClient(...)
stdClient := client.StandardClient()
```

---

## Request Body Rewindability

When retrying requests with bodies (e.g. `POST`, `PUT`, `PATCH`), standard Go readers consume the body stream on the first attempt.

`httpr` automatically preserves request bodies:
1. If `req.GetBody` is present, it calls it to reset the body for each retry attempt.
2. If `req.GetBody` is absent, it buffers the body into memory (up to `WithMaxBodyBytes`, default: 10 MB) and installs a rewindable `GetBody` handler automatically.

```go
client := httpr.NewClient(
    httpr.WithMaxBodyBytes(5 * 1024 * 1024), // 5 MB limit
)
```

---

## Configuration Options Summary

| Option | Default | Description |
|---|---|---|
| `WithMaxRetries(int)` | `3` | Max retry attempts (e.g. 3 retries = up to 4 total attempts). |
| `WithBackoff(Backoff)` | `ExponentialBackoff` | Custom backoff calculator. |
| `WithExponentialBackoff(...)` | `100ms`, `2s`, `2.0`, `FullJitter` | Helper to configure exponential backoff. |
| `WithLinearBackoff(...)` | - | Helper to configure linear backoff. |
| `WithConstantBackoff(...)` | - | Helper to configure constant backoff. |
| `WithRetryAfterHeader(bool)` | `true` | Whether to honor `Retry-After` response headers. |
| `WithRetryPolicy(RetryPolicy)` | `DefaultRetryPolicy` | Predicate determining whether to retry an attempt. |
| `WithCheckRetry(...)` | - | Inline alias for `WithRetryPolicy`. |
| `WithRetryStatusCodes(...)` | `408, 429, 500, 502, 503, 504` | Retry on specific HTTP status codes and network errors. |
| `WithPerAttemptTimeout(duration)` | `0` (none) | Per-attempt timeout without affecting overall context. |
| `WithMaxBodyBytes(int64)` | `10MB` | Maximum request body buffer for rewindability. |
| `WithTransport(RoundTripper)` | Tuned `*http.Transport` | Custom underlying HTTP transport. |
| `WithHTTPClient(*http.Client)` | `&http.Client{...}` | Custom underlying HTTP client. |
| `WithOnRetry(OnRetryHook)` | `nil` | Hook called before sleeping and executing a retry. |
| `WithAfterAttempt(AfterAttemptHook)`| `nil` | Hook called after each attempt finishes. |
| `WithCircuitBreaker(...)` | Disabled | Enable built-in SRE circuit breaker, auto-wrapping transport. |
| `WithCustomCircuitBreaker(...)` | Disabled | Enable custom `CircuitBreaker` implementation. |
| `WithCircuitBreakerClassifier(...)`| Default | Custom success/failure classification for circuit breaker. |

---

## License

MIT License
