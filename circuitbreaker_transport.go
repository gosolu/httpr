package httpr

import (
	"net/http"
)

// CircuitBreakerTransport wraps an underlying http.RoundTripper with a CircuitBreaker.
type CircuitBreakerTransport struct {
	base       http.RoundTripper
	breaker    CircuitBreaker
	classifier CircuitBreakerClassifier
}

// NewCircuitBreakerTransport creates an http.RoundTripper wrapped with a CircuitBreaker.
func NewCircuitBreakerTransport(base http.RoundTripper, cb CircuitBreaker, classifier CircuitBreakerClassifier) http.RoundTripper {
	if base == nil {
		base = defaultTransport()
	}
	if classifier == nil {
		classifier = DefaultCircuitBreakerClassifier
	}
	return &CircuitBreakerTransport{
		base:       base,
		breaker:    cb,
		classifier: classifier,
	}
}

// RoundTrip executes an HTTP request attempt through the circuit breaker.
// If the circuit breaker rejects the request, ErrCircuitOpen is returned immediately.
func (t *CircuitBreakerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.breaker != nil {
		if err := t.breaker.Allow(); err != nil {
			return nil, err
		}
	}

	resp, err := t.base.RoundTrip(req)

	if t.breaker != nil {
		if t.classifier(resp, err) {
			t.breaker.MarkFailed()
		} else {
			t.breaker.MarkSuccess()
		}
	}

	return resp, err
}
