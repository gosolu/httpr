package httpr

import (
	"net/http"
)

// RetryTransport implements http.RoundTripper to inject retry logic into any standard http.Client.
type RetryTransport struct {
	client *Client
}

// NewRoundTripper creates an http.RoundTripper that automatically retries failed requests
// using the provided httpr options.
func NewRoundTripper(opts ...Option) http.RoundTripper {
	client := NewClient(opts...)
	return NewRoundTripperWithClient(client)
}

// NewRoundTripperWithClient creates an http.RoundTripper that uses the given httpr Client.
func NewRoundTripperWithClient(client *Client) http.RoundTripper {
	if client == nil {
		client = NewClient()
	}
	return &RetryTransport{
		client: client,
	}
}

// RoundTrip executes a single HTTP transaction and retries transient failures.
// It implements http.RoundTripper.
func (t *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.client.Do(req.Context(), req)
}
