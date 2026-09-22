# AGENTS.md

## 1. Project Overview & Mission

`httpr` (`github.com/gosolu/httpr`) is a production-grade, lightweight Go HTTP client library designed for resilience, observability, and ergonomics. It provides automated retries, exponential backoff with jitter, a Google SRE adaptive throttling circuit breaker, rewindable request bodies, automated `httptrace` timing breakdowns, and seamless standard library `http.RoundTripper` integration.

### Core Design Principles

1. **Zero External Dependencies**:
   - Pure Go standard library (Go 1.22+).
   - **Do NOT introduce third-party dependencies** without explicit user consent. Everything must be implemented using standard Go packages (`net/http`, `context`, `sync`, `time`, `math/rand/v2`, `net/http/httptrace`, `io`, `bytes`, etc.).

2. **Simplicity over Bloat (Standard Library Proximity)**:
   - `httpr` is intentionally NOT a heavyweight framework or a Resty clone.
   - Keep standard library types at the core: `*http.Request`, `*http.Response`, and `http.RoundTripper`.
   - Avoid creating redundant wrapper structs or complex fluent builders unless specifically instructed.

3. **Context-First Design**:
   - All request methods (`Do`, `Get`, `Post`, `Put`, `Delete`, `Head`, `PostForm`) must accept `ctx context.Context` as their first parameter.
   - The provided `ctx` always takes precedence over `req.Context()`.
   - Cancellation and deadlines must be checked promptly across all retry loops and backoff pauses.

4. **Resource Safety & Connection Reuse**:
   - When retrying or abandoning an attempt, discarded response bodies must be drained and closed (`drainAndClose`) to allow HTTP keep-alive connection reuse without socket leaks.
   - Request bodies must be rewindable (`req.GetBody` or buffered memory reader up to `MaxBodyBytes`).

5. **Thread Safety**:
   - All `Client` instances and package-level global default functions (`httpr.Get`, `httpr.Post`, etc.) must be safe for concurrent use by multiple goroutines.

---

## 2. Codebase Map

| File | Purpose | Key Symbols & Responsibilities |
|---|---|---|
| [`client.go`](./client.go) | Core HTTP client & retry engine | `Client`, `NewClient`, `Client.Do`, `Get`, `Post`, `Put`, `Delete`, `Head`, `PostForm`, `StandardClient`. Implements attempt loop, per-attempt timeouts, context precedence, and body rewinding. |
| [`default.go`](./default.go) | Global singleton & package-level functions | `DefaultClient()`, `SetDefaultClient()`, package-level `Get`, `Post`, `Put`, `Delete`, `Head`, `PostForm`, `Do`. Lazily initialized using `sync.RWMutex`. |
| [`options.go`](./options.go) | Functional configuration options | `Options`, `Option`, `WithMaxRetries`, `WithBackoff`, `WithRetryPolicy`, `WithTransport`, `WithCircuitBreaker`, `WithPerAttemptTimeout`, `WithOnRetry`, `WithTrace`. |
| [`trace.go`](./trace.go) | Automated HTTP request tracing | `TraceInfo`, `TraceHook`, `newTraceCollector`. Automatically computes DNS, Connect, TLS, TTFB (WaitDuration), and connection reuse metrics using `net/http/httptrace`. |
| [`backoff.go`](./backoff.go) | Backoff strategies & jitter | `Backoff` interface, `ExponentialBackoff` (`FullJitter`, `EqualJitter`, `NoJitter`), `LinearBackoff`, `ConstantBackoff`, `ParseRetryAfter` (RFC1123 & seconds). |
| [`policy.go`](./policy.go) | Retry qualification policies | `RetryPolicy` signature, `DefaultRetryPolicy` (retries 5xx, 429, 408, network errors; halts on `ErrCircuitOpen`), `RetryOnStatusCodes`, `CombinePolicies`. |
| [`circuitbreaker.go`](./circuitbreaker.go) | Google SRE adaptive throttling | `CircuitBreaker` interface, `ErrCircuitOpen`, `SREBreaker` sliding window bucket counter, probabilistic drop formula: `max(0, (requests - K*accepts)/(requests + 1))`. |
| [`circuitbreaker_transport.go`](./circuitbreaker_transport.go) | Inner transport wrapper | `circuitBreakerTransport` wrapping `http.RoundTripper`, `DefaultCircuitBreakerClassifier`. |
| [`request.go`](./request.go) | Request/response streaming helpers | `prepareRequest`, `rewindBody`, `drainAndClose`. |
| [`transport.go`](./transport.go) | `http.RoundTripper` integration | `RetryTransport`, `NewRoundTripper`, `NewRoundTripperWithClient` to inject retry capabilities into any standard `*http.Client`. |
| [`*_test.go`](./) | Unit & concurrency test suite | Table-driven tests, mock round-trippers, race detection, and coverage verifications. |

---

## 3. Development Commands & Workflow

### Running Tests
Always run the test suite with the race detector and coverage tracking:
```bash
go test -v -race -cover ./...
```

### Static Analysis & Verification
Ensure code is statically sound:
```bash
go vet ./...
```

### Formatting
Always format Go code using standard tools before committing:
```bash
gofmt -s -w .
```

### Quality Standards
- **Test Coverage**: Maintain statement coverage **>= 90%**.
- **Zero Race Conditions**: `-race` must always pass cleanly.
- **Backward Compatibility**: Existing public APIs must not be broken.

---

## 4. Coding Conventions & Best Practices

### Error Handling
- Use prefixed errors for clear context: `fmt.Errorf("httpr: <context>: %w", err)`.
- Use sentinel errors where appropriate (`ErrCircuitOpen`).
- When a retry loop exhausts attempts, return a `*RetryError` containing `Attempts`, `MaxRetries`, `LastErr`, and `Response`.

### Circuit Breaker & Retries
- The circuit breaker is an **inner transport wrapper** (`circuitBreakerTransport`).
- If the breaker throttles a request, it returns `ErrCircuitOpen`.
- `DefaultRetryPolicy` checks `errors.Is(err, ErrCircuitOpen)` and immediately returns `false, err` to fail fast and prevent retry storms.

### Git Commits
Follow the Conventional Commits specification:
- `feat: add ...`
- `fix: resolve ...`
- `refactor: simplify ...`
- `test: add unit test for ...`
- `docs: update ...`
