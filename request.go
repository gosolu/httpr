package httpr

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
)

const (
	// DefaultMaxBodySize is the maximum request body size (10 MB) buffered in memory
	// if req.GetBody is not already provided.
	DefaultMaxBodySize = 10 * 1024 * 1024
)

// prepareRequest ensures that the request body can be rewound and re-read across retries.
// If req.GetBody is not defined but req.Body is present, it reads the body up to maxBodyBytes
// into a memory buffer and defines a suitable GetBody function.
func prepareRequest(req *http.Request, maxBodyBytes int64) error {
	if req == nil || req.Body == nil || req.Body == http.NoBody {
		return nil
	}

	if req.GetBody != nil {
		return nil
	}

	if maxBodyBytes <= 0 {
		maxBodyBytes = DefaultMaxBodySize
	}

	// Read body up to maxBodyBytes + 1 to detect truncation
	limitedReader := io.LimitReader(req.Body, maxBodyBytes+1)
	buf, err := io.ReadAll(limitedReader)
	_ = req.Body.Close()
	if err != nil {
		return fmt.Errorf("failed to read request body for retry preparation: %w", err)
	}

	if int64(len(buf)) > maxBodyBytes {
		return fmt.Errorf("request body size exceeds maximum retry buffer limit (%d bytes)", maxBodyBytes)
	}

	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(buf)), nil
	}
	req.Body, _ = req.GetBody()

	return nil
}

// rewindBody resets req.Body to the beginning using req.GetBody.
func rewindBody(req *http.Request) error {
	if req == nil || req.GetBody == nil {
		return nil
	}
	body, err := req.GetBody()
	if err != nil {
		return fmt.Errorf("failed to rewind request body: %w", err)
	}
	req.Body = body
	return nil
}

// drainAndClose safely drains a response body up to 8KB to allow HTTP connection reuse,
// and then closes the body.
func drainAndClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8192))
	_ = resp.Body.Close()
}
