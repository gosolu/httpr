package httpr

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"sync"
)

var (
	defaultClientMu sync.RWMutex
	defaultClient   *Client
)

// DefaultClient returns the global default Client instance.
// If not already initialized, it lazily creates and returns a new default Client.
func DefaultClient() *Client {
	defaultClientMu.RLock()
	c := defaultClient
	defaultClientMu.RUnlock()
	if c != nil {
		return c
	}

	defaultClientMu.Lock()
	defer defaultClientMu.Unlock()
	if defaultClient == nil {
		defaultClient = NewClient()
	}
	return defaultClient
}

// SetDefaultClient sets or overrides the global default Client instance.
// Passing nil resets the default client so the next call to DefaultClient
// will recreate a fresh default Client.
func SetDefaultClient(c *Client) {
	defaultClientMu.Lock()
	defer defaultClientMu.Unlock()
	defaultClient = c
}

// Do issues an HTTP request using the global default Client.
func Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	return DefaultClient().Do(ctx, req)
}

// Get issues a GET request to the specified URL using the global default Client.
func Get(ctx context.Context, url string) (*http.Response, error) {
	return DefaultClient().Get(ctx, url)
}

// Head issues a HEAD request to the specified URL using the global default Client.
func Head(ctx context.Context, url string) (*http.Response, error) {
	return DefaultClient().Head(ctx, url)
}

// Post issues a POST request with the specified body to the target URL using the global default Client.
func Post(ctx context.Context, url, contentType string, body io.Reader) (*http.Response, error) {
	return DefaultClient().Post(ctx, url, contentType, body)
}

// Put issues a PUT request with the specified body to the target URL using the global default Client.
func Put(ctx context.Context, url, contentType string, body io.Reader) (*http.Response, error) {
	return DefaultClient().Put(ctx, url, contentType, body)
}

// Delete issues a DELETE request to the specified URL using the global default Client.
func Delete(ctx context.Context, url string) (*http.Response, error) {
	return DefaultClient().Delete(ctx, url)
}

// PostForm issues a POST with form urlencoded data using the global default Client.
func PostForm(ctx context.Context, targetURL string, data url.Values) (*http.Response, error) {
	return DefaultClient().PostForm(ctx, targetURL, data)
}
