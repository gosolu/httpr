package httpr

import (
	"context"
	"net"
	"net/http"
	"time"
)

// OnRetryHook is invoked immediately before waiting and executing a retry attempt.
type OnRetryHook func(attempt int, req *http.Request, resp *http.Response, err error, wait time.Duration)

// AfterAttemptHook is invoked after every request attempt finishes (regardless of success or retry).
type AfterAttemptHook func(attempt int, req *http.Request, resp *http.Response, err error)

// Options holds all configurable parameters for httpr Client and Transport.
type Options struct {
	// MaxRetries is the maximum number of retry attempts after the initial request.
	// Default is 3 (meaning up to 4 total attempts).
	MaxRetries int

	// Backoff calculates the pause duration before a retry attempt.
	// Default is ExponentialBackoff with FullJitter.
	Backoff Backoff

	// RetryPolicy determines if an attempt qualifies for a retry.
	// Default is DefaultRetryPolicy.
	RetryPolicy RetryPolicy

	// HTTPClient is the underlying standard http.Client used to execute requests.
	HTTPClient *http.Client

	// PerAttemptTimeout defines a timeout applied to each individual attempt.
	// If 0, attempts inherit the request's context deadline.
	PerAttemptTimeout time.Duration

	// MaxBodyBytes is the maximum request body size buffered in memory for rewindable retries.
	// Default is 10 MB.
	MaxBodyBytes int64

	// OnRetry is called before each retry backoff pause.
	OnRetry OnRetryHook

	// AfterAttempt is called after each request attempt completes.
	AfterAttempt AfterAttemptHook

	// Logger logs debug/retry messages. Default is NoopLogger.
	Logger Logger

	// CircuitBreakerEnabled indicates whether the inner circuit breaker is enabled.
	CircuitBreakerEnabled bool

	// CircuitBreaker is the circuit breaker instance used to protect HTTP attempts.
	CircuitBreaker CircuitBreaker

	// CircuitBreakerClassifier classifies attempt outcomes as success or failure for the circuit breaker.
	CircuitBreakerClassifier CircuitBreakerClassifier
}

// Option configures an Options struct.
type Option func(*Options)

// DefaultOptions returns the production-ready default options for httpr.
func DefaultOptions() *Options {
	return &Options{
		MaxRetries:        3,
		Backoff:           NewExponentialBackoff(),
		RetryPolicy:       DefaultRetryPolicy,
		HTTPClient:        &http.Client{Transport: defaultTransport()},
		PerAttemptTimeout: 0,
		MaxBodyBytes:      DefaultMaxBodySize,
		Logger:            NoopLogger{},
	}
}

// defaultTransport provides an optimized http.Transport with connection pooling.
func defaultTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// WithMaxRetries sets the maximum number of retry attempts.
// For example, WithMaxRetries(3) allows up to 1 initial attempt + 3 retries.
func WithMaxRetries(n int) Option {
	return func(o *Options) {
		if n < 0 {
			n = 0
		}
		o.MaxRetries = n
	}
}

// WithBackoff sets a custom Backoff strategy.
func WithBackoff(b Backoff) Option {
	return func(o *Options) {
		if b != nil {
			o.Backoff = b
		}
	}
}

// WithExponentialBackoff sets an exponential backoff strategy with custom parameters.
func WithExponentialBackoff(minWait, maxWait time.Duration, factor float64, jitter JitterType) Option {
	return func(o *Options) {
		o.Backoff = &ExponentialBackoff{
			MinWait:         minWait,
			MaxWait:         maxWait,
			Factor:          factor,
			Jitter:          jitter,
			CheckRetryAfter: true,
		}
	}
}

// WithLinearBackoff sets a linear backoff strategy.
func WithLinearBackoff(minWait, step, maxWait time.Duration, jitter bool) Option {
	return func(o *Options) {
		o.Backoff = &LinearBackoff{
			MinWait:         minWait,
			Step:            step,
			MaxWait:         maxWait,
			Jitter:          jitter,
			CheckRetryAfter: true,
		}
	}
}

// WithConstantBackoff sets a constant interval backoff strategy.
func WithConstantBackoff(interval time.Duration, jitter bool) Option {
	return func(o *Options) {
		o.Backoff = &ConstantBackoff{
			Interval:        interval,
			Jitter:          jitter,
			CheckRetryAfter: true,
		}
	}
}

// WithRetryPolicy sets a custom RetryPolicy function.
func WithRetryPolicy(p RetryPolicy) Option {
	return func(o *Options) {
		if p != nil {
			o.RetryPolicy = p
		}
	}
}

// WithCheckRetry is an alias for WithRetryPolicy allowing an inline callback.
func WithCheckRetry(fn func(ctx context.Context, resp *http.Response, err error) (bool, error)) Option {
	return WithRetryPolicy(fn)
}

// WithRetryStatusCodes configures the client to retry on the specified HTTP status codes,
// combining with transient network error retries.
func WithRetryStatusCodes(statusCodes ...int) Option {
	return func(o *Options) {
		o.RetryPolicy = CombinePolicies(RetryOnStatusCodes(statusCodes...), RetryOnNetworkErrors())
	}
}

// WithRetryAfterHeader controls whether backoff strategies inspect and respect the Retry-After header.
func WithRetryAfterHeader(enabled bool) Option {
	return func(o *Options) {
		switch b := o.Backoff.(type) {
		case *ExponentialBackoff:
			b.CheckRetryAfter = enabled
		case *LinearBackoff:
			b.CheckRetryAfter = enabled
		case *ConstantBackoff:
			b.CheckRetryAfter = enabled
		}
	}
}

// WithHTTPClient provides a custom standard library http.Client.
func WithHTTPClient(client *http.Client) Option {
	return func(o *Options) {
		if client != nil {
			o.HTTPClient = client
		}
	}
}

// WithTransport configures a custom http.RoundTripper transport on the underlying client.
func WithTransport(rt http.RoundTripper) Option {
	return func(o *Options) {
		if o.HTTPClient == nil {
			o.HTTPClient = &http.Client{}
		}
		o.HTTPClient.Transport = rt
	}
}

// WithPerAttemptTimeout sets a timeout for each individual HTTP request attempt.
func WithPerAttemptTimeout(d time.Duration) Option {
	return func(o *Options) {
		o.PerAttemptTimeout = d
	}
}

// WithMaxBodyBytes sets the maximum payload size in bytes buffered into memory for rewindable retries.
func WithMaxBodyBytes(n int64) Option {
	return func(o *Options) {
		if n > 0 {
			o.MaxBodyBytes = n
		}
	}
}

// WithOnRetry registers a hook called before sleeping and retrying.
func WithOnRetry(hook OnRetryHook) Option {
	return func(o *Options) {
		o.OnRetry = hook
	}
}

// WithAfterAttempt registers a hook called after each attempt finishes.
func WithAfterAttempt(hook AfterAttemptHook) Option {
	return func(o *Options) {
		o.AfterAttempt = hook
	}
}

// WithLogger configures a logger for retry activity.
func WithLogger(l Logger) Option {
	return func(o *Options) {
		if l != nil {
			o.Logger = l
		}
	}
}

// WithCircuitBreaker enables the built-in SRE circuit breaker, automatically wrapping
// the client's internal HTTP transport.
func WithCircuitBreaker(opts ...CircuitBreakerOption) Option {
	cfg := DefaultCircuitBreakerConfig()
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	breaker := NewSREBreaker(opts...)
	return func(o *Options) {
		o.CircuitBreakerEnabled = true
		o.CircuitBreaker = breaker
		o.CircuitBreakerClassifier = cfg.Classifier
	}
}

// WithCustomCircuitBreaker enables a custom CircuitBreaker implementation, automatically
// wrapping the client's internal HTTP transport.
func WithCustomCircuitBreaker(cb CircuitBreaker, classifier ...CircuitBreakerClassifier) Option {
	var c CircuitBreakerClassifier = DefaultCircuitBreakerClassifier
	if len(classifier) > 0 && classifier[0] != nil {
		c = classifier[0]
	}
	return func(o *Options) {
		if cb != nil {
			o.CircuitBreakerEnabled = true
			o.CircuitBreaker = cb
			o.CircuitBreakerClassifier = c
		}
	}
}

// WithCircuitBreakerClassifier sets a custom failure classifier for the circuit breaker.
func WithCircuitBreakerClassifier(fn CircuitBreakerClassifier) Option {
	return func(o *Options) {
		if fn != nil {
			o.CircuitBreakerClassifier = fn
		}
	}
}
