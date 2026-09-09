package httpr

import (
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

// JitterType defines the randomization strategy applied to backoff durations.
type JitterType int

const (
	// NoJitter applies no randomization; wait times are deterministic.
	NoJitter JitterType = iota
	// FullJitter yields a uniform random duration between 0 and the calculated wait time.
	FullJitter
	// EqualJitter yields wait/2 + uniform random duration between 0 and wait/2.
	EqualJitter
)

// Backoff defines the strategy used to calculate how long to pause between retries.
type Backoff interface {
	// NextBackoff returns the duration to wait before the specified retry attempt (1-indexed).
	// The response is passed to allow inspection of headers like Retry-After.
	NextBackoff(attempt int, resp *http.Response) time.Duration
}

// ExponentialBackoff implements an exponential backoff strategy with configurable jitter.
type ExponentialBackoff struct {
	// MinWait is the initial wait duration for the first retry. Default is 100ms.
	MinWait time.Duration
	// MaxWait is the maximum wait duration cap. Default is 2s.
	MaxWait time.Duration
	// Factor is the multiplier applied for each subsequent retry. Default is 2.0.
	Factor float64
	// Jitter is the randomization strategy to prevent thundering herd problems. Default is FullJitter.
	Jitter JitterType
	// CheckRetryAfter, if true, inspects the HTTP Retry-After header and prefers it if valid.
	CheckRetryAfter bool
}

// NewExponentialBackoff creates an ExponentialBackoff with default values:
// MinWait: 100ms, MaxWait: 2s, Factor: 2.0, Jitter: FullJitter, CheckRetryAfter: true.
func NewExponentialBackoff() *ExponentialBackoff {
	return &ExponentialBackoff{
		MinWait:         100 * time.Millisecond,
		MaxWait:         2 * time.Second,
		Factor:          2.0,
		Jitter:          FullJitter,
		CheckRetryAfter: true,
	}
}

// NextBackoff calculates the wait duration for the given attempt.
func (e *ExponentialBackoff) NextBackoff(attempt int, resp *http.Response) time.Duration {
	if e.CheckRetryAfter && resp != nil {
		if dur, ok := ParseRetryAfter(resp); ok {
			if e.MaxWait > 0 && dur > e.MaxWait {
				return e.MaxWait
			}
			return dur
		}
	}

	minWait := e.MinWait
	if minWait <= 0 {
		minWait = 100 * time.Millisecond
	}
	maxWait := e.MaxWait
	if maxWait <= 0 {
		maxWait = 2 * time.Second
	}
	if maxWait < minWait {
		maxWait = minWait
	}

	factor := e.Factor
	if factor <= 1.0 {
		factor = 2.0
	}

	if attempt < 1 {
		attempt = 1
	}

	// Calculate base: minWait * factor^(attempt - 1)
	multiplier := math.Pow(factor, float64(attempt-1))
	var wait float64
	if multiplier > float64(maxWait/minWait) {
		wait = float64(maxWait)
	} else {
		wait = float64(minWait) * multiplier
	}

	if wait > float64(maxWait) {
		wait = float64(maxWait)
	}

	switch e.Jitter {
	case FullJitter:
		if wait > 0 {
			wait = rand.Float64() * wait
		}
	case EqualJitter:
		half := wait / 2.0
		if half > 0 {
			wait = half + (rand.Float64() * half)
		}
	case NoJitter:
		// wait unchanged
	}

	dur := time.Duration(wait)
	if dur > maxWait {
		dur = maxWait
	}
	return dur
}

// LinearBackoff implements a linear backoff strategy: wait = MinWait + (attempt - 1) * Step.
type LinearBackoff struct {
	MinWait         time.Duration
	Step            time.Duration
	MaxWait         time.Duration
	Jitter          bool
	CheckRetryAfter bool
}

// NewLinearBackoff creates a LinearBackoff with default values.
func NewLinearBackoff(minWait, step, maxWait time.Duration) *LinearBackoff {
	return &LinearBackoff{
		MinWait:         minWait,
		Step:            step,
		MaxWait:         maxWait,
		Jitter:          false,
		CheckRetryAfter: true,
	}
}

// NextBackoff calculates linear wait duration.
func (l *LinearBackoff) NextBackoff(attempt int, resp *http.Response) time.Duration {
	if l.CheckRetryAfter && resp != nil {
		if dur, ok := ParseRetryAfter(resp); ok {
			if l.MaxWait > 0 && dur > l.MaxWait {
				return l.MaxWait
			}
			return dur
		}
	}

	if attempt < 1 {
		attempt = 1
	}

	wait := l.MinWait + time.Duration(attempt-1)*l.Step
	if l.MaxWait > 0 && wait > l.MaxWait {
		wait = l.MaxWait
	}

	if l.Jitter && wait > 0 {
		wait = time.Duration(rand.Float64() * float64(wait))
	}

	return wait
}

// ConstantBackoff implements a constant wait interval between retries.
type ConstantBackoff struct {
	Interval        time.Duration
	Jitter          bool
	CheckRetryAfter bool
}

// NewConstantBackoff creates a ConstantBackoff strategy.
func NewConstantBackoff(interval time.Duration) *ConstantBackoff {
	return &ConstantBackoff{
		Interval:        interval,
		Jitter:          false,
		CheckRetryAfter: true,
	}
}

// NextBackoff returns the constant wait duration.
func (c *ConstantBackoff) NextBackoff(attempt int, resp *http.Response) time.Duration {
	if c.CheckRetryAfter && resp != nil {
		if dur, ok := ParseRetryAfter(resp); ok {
			return dur
		}
	}

	wait := c.Interval
	if c.Jitter && wait > 0 {
		wait = time.Duration(rand.Float64() * float64(wait))
	}
	return wait
}

// ParseRetryAfter parses the standard Retry-After header from an HTTP response.
// It supports either integer seconds or HTTP-date formats (RFC1123, RFC850, ANSIC).
func ParseRetryAfter(resp *http.Response) (time.Duration, bool) {
	if resp == nil || resp.Header == nil {
		return 0, false
	}

	val := resp.Header.Get("Retry-After")
	if val == "" {
		return 0, false
	}

	// Try integer seconds
	if seconds, err := strconv.ParseInt(val, 10, 64); err == nil {
		if seconds < 0 {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}

	// Try HTTP-date formats
	dateFormats := []string{
		time.RFC1123,
		time.RFC1123Z,
		time.RFC850,
		time.ANSIC,
	}

	for _, format := range dateFormats {
		if t, err := time.Parse(format, val); err == nil {
			dur := time.Until(t)
			if dur < 0 {
				dur = 0
			}
			return dur, true
		}
	}

	return 0, false
}
