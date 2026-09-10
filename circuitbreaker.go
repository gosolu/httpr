package httpr

import (
	"errors"
	"math"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"
)

// ErrCircuitOpen is returned when a request is rejected because the circuit breaker is open or throttling.
var ErrCircuitOpen = errors.New("httpr: circuit breaker open")

// CircuitBreaker defines the circuit breaker interface adapted from go-kratos/aegis/circuitbreaker.
type CircuitBreaker interface {
	// Allow checks whether a request is allowed to proceed.
	// Returns nil if allowed, or ErrCircuitOpen if rejected.
	Allow() error
	// MarkSuccess reports a successful request to the circuit breaker.
	MarkSuccess()
	// MarkFailed reports a failed request to the circuit breaker.
	MarkFailed()
}

// CircuitBreakerClassifier determines whether an attempt's response or error should be counted
// as a circuit breaker failure.
type CircuitBreakerClassifier func(resp *http.Response, err error) bool

// DefaultCircuitBreakerClassifier counts network errors and HTTP 5xx responses as failures.
// Successful responses (2xx, 3xx) and standard client errors (4xx) are treated as successes.
func DefaultCircuitBreakerClassifier(resp *http.Response, err error) bool {
	if err != nil {
		if errors.Is(err, ErrCircuitOpen) {
			return false
		}
		return true
	}
	if resp != nil && resp.StatusCode >= 500 {
		return true
	}
	return false
}

// CircuitBreakerConfig holds configuration parameters for the SRE circuit breaker.
type CircuitBreakerConfig struct {
	// SuccessRatio is the targeted success ratio (K = 1 / SuccessRatio).
	// Default is 0.6 (K ≈ 1.67).
	// Decreasing SuccessRatio makes throttling more aggressive.
	SuccessRatio float64

	// MinRequests is the minimum number of requests in the window before throttling activates.
	// Default is 20.
	MinRequests int64

	// Window is the statistical rolling window duration.
	// Default is 5s.
	Window time.Duration

	// Buckets is the number of sliding buckets within the window.
	// Default is 10.
	Buckets int

	// Classifier determines if an attempt is considered a failure.
	Classifier CircuitBreakerClassifier
}

// CircuitBreakerOption configures a CircuitBreakerConfig.
type CircuitBreakerOption func(*CircuitBreakerConfig)

// WithSuccessRatio sets the K = 1 / SuccessRatio target for the SRE circuit breaker.
func WithSuccessRatio(ratio float64) CircuitBreakerOption {
	return func(c *CircuitBreakerConfig) {
		if ratio > 0 && ratio <= 1.0 {
			c.SuccessRatio = ratio
		}
	}
}

// WithMinRequests sets the minimum number of requests in the window before throttling evaluates.
func WithMinRequests(n int64) CircuitBreakerOption {
	return func(c *CircuitBreakerConfig) {
		if n > 0 {
			c.MinRequests = n
		}
	}
}

// WithWindow sets the statistical sliding window duration.
func WithWindow(d time.Duration) CircuitBreakerOption {
	return func(c *CircuitBreakerConfig) {
		if d > 0 {
			c.Window = d
		}
	}
}

// WithBuckets sets the number of buckets in the sliding window.
func WithBuckets(b int) CircuitBreakerOption {
	return func(c *CircuitBreakerConfig) {
		if b > 0 {
			c.Buckets = b
		}
	}
}

// WithFailureClassifier sets a custom function to classify HTTP attempts as success or failure.
func WithFailureClassifier(fn CircuitBreakerClassifier) CircuitBreakerOption {
	return func(c *CircuitBreakerConfig) {
		if fn != nil {
			c.Classifier = fn
		}
	}
}

// DefaultCircuitBreakerConfig returns default SRE circuit breaker settings.
func DefaultCircuitBreakerConfig() *CircuitBreakerConfig {
	return &CircuitBreakerConfig{
		SuccessRatio: 0.6,
		MinRequests:  20,
		Window:       5 * time.Second,
		Buckets:      10,
		Classifier:   DefaultCircuitBreakerClassifier,
	}
}

// SREBreaker implements the Google SRE client-side adaptive throttling circuit breaker
// (Google SRE Book, Chapter 21: Handling Overload - Client-Side Throttling).
type SREBreaker struct {
	mu          sync.Mutex
	k           float64
	minRequests int64
	window      *rollingWindow
}

// NewSREBreaker creates a new SREBreaker with optional configuration.
func NewSREBreaker(opts ...CircuitBreakerOption) *SREBreaker {
	cfg := DefaultCircuitBreakerConfig()
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}

	bucketDuration := cfg.Window / time.Duration(cfg.Buckets)
	if bucketDuration <= 0 {
		bucketDuration = 500 * time.Millisecond
	}

	return &SREBreaker{
		k:           1.0 / cfg.SuccessRatio,
		minRequests: cfg.MinRequests,
		window:      newRollingWindow(cfg.Buckets, bucketDuration),
	}
}

// Allow checks if the request is allowed according to the SRE adaptive throttling algorithm.
func (b *SREBreaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	successes, total := b.window.Summary()

	// The number of requests accepted by the backend multiplied by K
	accepted := b.k * float64(successes)

	// If total requests in the window are below the threshold, or total < K*successes, allow
	if total < b.minRequests || float64(total) < accepted {
		return nil
	}

	// Drop probability = max(0, (total - K*successes) / (total + 1))
	dropProb := math.Max(0, (float64(total)-accepted)/float64(total+1))
	if dropProb <= 0 {
		return nil
	}

	if rand.Float64() < dropProb {
		return ErrCircuitOpen
	}

	return nil
}

// MarkSuccess marks the request as successful in the circuit breaker's window.
func (b *SREBreaker) MarkSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.window.Add(1, 1) // 1 success, 1 total
}

// MarkFailed marks the request as failed in the circuit breaker's window.
func (b *SREBreaker) MarkFailed() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.window.Add(0, 1) // 0 success, 1 total
}

// rollingBucket holds counts for a single time slice.
type rollingBucket struct {
	success int64
	total   int64
}

// rollingWindow manages a ring buffer of buckets moving with time.
type rollingWindow struct {
	buckets        []rollingBucket
	size           int
	bucketDuration time.Duration
	lastTime       time.Time
	offset         int
}

func newRollingWindow(size int, bucketDuration time.Duration) *rollingWindow {
	if size <= 0 {
		size = 10
	}
	return &rollingWindow{
		buckets:        make([]rollingBucket, size),
		size:           size,
		bucketDuration: bucketDuration,
		lastTime:       time.Now(),
		offset:         0,
	}
}

// advance advances the window buckets according to elapsed time.
func (w *rollingWindow) advance() {
	now := time.Now()
	elapsed := now.Sub(w.lastTime)
	if elapsed < w.bucketDuration {
		return
	}

	timespan := int(elapsed / w.bucketDuration)
	if timespan > w.size {
		timespan = w.size
	}

	for i := 0; i < timespan; i++ {
		w.offset = (w.offset + 1) % w.size
		w.buckets[w.offset] = rollingBucket{}
	}

	w.lastTime = now
}

// Add increments the current bucket with the given values.
func (w *rollingWindow) Add(success, total int64) {
	w.advance()
	w.buckets[w.offset].success += success
	w.buckets[w.offset].total += total
}

// Summary sums up the successes and totals across all active buckets in the window.
func (w *rollingWindow) Summary() (success int64, total int64) {
	w.advance()
	for _, b := range w.buckets {
		success += b.success
		total += b.total
	}
	return success, total
}
