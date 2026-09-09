package httpr

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPrepareRequest_NilOrNoBody(t *testing.T) {
	if err := prepareRequest(nil, 1024); err != nil {
		t.Fatalf("unexpected error for nil request: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", http.NoBody)
	if err := prepareRequest(req, 1024); err != nil {
		t.Fatalf("unexpected error for NoBody: %v", err)
	}
}

func TestPrepareRequest_RewindableReader(t *testing.T) {
	// io.NopCloser wrapping strings.Reader does not have req.GetBody set initially if constructed manually
	bodyStr := "hello httpr payload"
	req := &http.Request{
		Method: http.MethodPost,
		Body:   io.NopCloser(strings.NewReader(bodyStr)),
	}

	if err := prepareRequest(req, 1024); err != nil {
		t.Fatalf("prepareRequest failed: %v", err)
	}

	if req.GetBody == nil {
		t.Fatalf("expected req.GetBody to be set")
	}

	// Read first time
	b1, err := io.ReadAll(req.Body)
	if err != nil || string(b1) != bodyStr {
		t.Fatalf("first read got %s, %v", string(b1), err)
	}

	// Rewind and read second time
	if err := rewindBody(req); err != nil {
		t.Fatalf("rewindBody failed: %v", err)
	}
	b2, err := io.ReadAll(req.Body)
	if err != nil || string(b2) != bodyStr {
		t.Fatalf("second read got %s, %v", string(b2), err)
	}
}

func TestPrepareRequest_ExceedsMaxBody(t *testing.T) {
	req := &http.Request{
		Method: http.MethodPost,
		Body:   io.NopCloser(bytes.NewReader(make([]byte, 100))),
	}

	err := prepareRequest(req, 50) // limit is 50 bytes, payload is 100
	if err == nil {
		t.Fatalf("expected error exceeding max body limit, got nil")
	}
}

func TestDrainAndClose(t *testing.T) {
	// drainAndClose nil response
	drainAndClose(nil)

	// drainAndClose normal response
	closed := false
	r := io.NopCloser(strings.NewReader("some response content"))
	resp := &http.Response{
		Body: &trackingReadCloser{
			ReadCloser: r,
			onClose:    func() { closed = true },
		},
	}

	drainAndClose(resp)
	if !closed {
		t.Errorf("expected response body to be closed")
	}
}

type trackingReadCloser struct {
	io.ReadCloser
	onClose func()
}

func (t *trackingReadCloser) Close() error {
	if t.onClose != nil {
		t.onClose()
	}
	return t.ReadCloser.Close()
}
