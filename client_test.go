package httpr

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func stringResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Status:     http.StatusText(statusCode),
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
		Header:     make(http.Header),
	}
}

func TestClient_SuccessFirstAttempt(t *testing.T) {
	mock := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusOK, "ok"), nil
	})

	client := NewClient(
		WithMaxRetries(3),
		WithTransport(mock),
		WithBackoff(NewConstantBackoff(time.Millisecond)),
	)

	resp, err := client.Get(context.Background(), "http://example.com/test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("expected body 'ok', got '%s'", string(body))
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestClient_RetryOn500ThenSuccess(t *testing.T) {
	var attempts int32
	mock := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			return stringResponse(http.StatusInternalServerError, "server error"), nil
		}
		return stringResponse(http.StatusOK, "recovered"), nil
	})

	var retriesLogged int32
	var afterAttempts int32
	client := NewClient(
		WithMaxRetries(3),
		WithTransport(mock),
		WithBackoff(NewConstantBackoff(time.Millisecond)),
		WithOnRetry(func(attempt int, req *http.Request, resp *http.Response, err error, wait time.Duration) {
			atomic.AddInt32(&retriesLogged, 1)
		}),
		WithAfterAttempt(func(attempt int, req *http.Request, resp *http.Response, err error) {
			atomic.AddInt32(&afterAttempts, 1)
		}),
	)

	resp, err := client.Get(context.Background(), "http://example.com/retry-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
	if atomic.LoadInt32(&retriesLogged) != 2 {
		t.Errorf("expected 2 onRetry calls, got %d", retriesLogged)
	}
	if atomic.LoadInt32(&afterAttempts) != 3 {
		t.Errorf("expected 3 afterAttempt calls, got %d", afterAttempts)
	}
}

func TestClient_ExhaustRetries_StatusCode500(t *testing.T) {
	var attempts int32
	mock := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&attempts, 1)
		return stringResponse(http.StatusBadGateway, "bad gateway"), nil
	})

	client := NewClient(
		WithMaxRetries(2),
		WithTransport(mock),
		WithBackoff(NewConstantBackoff(time.Millisecond)),
	)

	resp, err := client.Get(context.Background(), "http://example.com/bad-gateway")
	if err != nil {
		t.Fatalf("unexpected error for 502 status: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("expected status 502, got %d", resp.StatusCode)
	}
	// Initial attempt + 2 retries = 3 attempts
	if atomic.LoadInt32(&attempts) != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestClient_ExhaustRetries_NetworkError(t *testing.T) {
	var attempts int32
	mock := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&attempts, 1)
		return nil, &net.OpError{
			Op:  "dial",
			Net: "tcp",
			Err: errors.New("connection refused"),
		}
	})

	client := NewClient(
		WithMaxRetries(2),
		WithTransport(mock),
		WithBackoff(NewConstantBackoff(time.Millisecond)),
	)

	resp, err := client.Get(context.Background(), "http://example.com/net-fail")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if resp != nil {
		t.Errorf("expected nil response on network failure")
	}

	var retryErr *RetryError
	if !errors.As(err, &retryErr) {
		t.Fatalf("expected error to be *RetryError, got %T: %v", err, err)
	}
	if retryErr.Attempts != 3 {
		t.Errorf("expected 3 attempts recorded, got %d", retryErr.Attempts)
	}
	if retryErr.MaxRetries != 2 {
		t.Errorf("expected maxRetries 2, got %d", retryErr.MaxRetries)
	}
	if retryErr.Unwrap() == nil {
		t.Errorf("expected unwrapped error to be non-nil")
	}
}

// customReader does NOT have GetBody, testing our memory buffer rewind mechanism
type customReader struct {
	content []byte
	offset  int
}

func (r *customReader) Read(p []byte) (n int, err error) {
	if r.offset >= len(r.content) {
		return 0, io.EOF
	}
	n = copy(p, r.content[r.offset:])
	r.offset += n
	return n, nil
}

func (r *customReader) Close() error {
	return nil
}

func TestClient_RequestBodyRewind(t *testing.T) {
	var mu sync.Mutex
	var receivedBodies []string

	mock := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(req.Body)
		mu.Lock()
		receivedBodies = append(receivedBodies, string(b))
		count := len(receivedBodies)
		mu.Unlock()

		if count < 3 {
			return stringResponse(http.StatusServiceUnavailable, "temporarily unavailable"), nil
		}
		return stringResponse(http.StatusOK, "ok"), nil
	})

	client := NewClient(
		WithMaxRetries(3),
		WithTransport(mock),
		WithBackoff(NewConstantBackoff(time.Millisecond)),
	)

	payload := "important json payload: {id: 42}"
	reader := &customReader{content: []byte(payload)}

	req, err := http.NewRequest(http.MethodPost, "http://example.com/rewind", reader)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	if req.GetBody != nil {
		t.Fatalf("expected req.GetBody to be nil for custom reader before prepare")
	}

	resp, err := client.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(receivedBodies) != 3 {
		t.Fatalf("expected 3 requests received, got %d", len(receivedBodies))
	}
	for i, b := range receivedBodies {
		if b != payload {
			t.Errorf("attempt %d received '%s', want '%s'", i+1, b, payload)
		}
	}
}

func TestClient_ContextCancellation(t *testing.T) {
	mock := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusInternalServerError, "server error"), nil
	})

	client := NewClient(
		WithMaxRetries(5),
		WithTransport(mock),
		WithBackoff(NewConstantBackoff(500 * time.Millisecond)),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	req, _ := http.NewRequest(http.MethodGet, "http://example.com/cancel", nil)
	start := time.Now()
	_, err := client.Do(ctx, req)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed > 300*time.Millisecond {
		t.Errorf("request took too long to abort on cancel: %v", elapsed)
	}
}

func TestClient_UsesProvidedContextInsteadOfRequestContext(t *testing.T) {
	mock := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusOK, "ok"), nil
	})

	client := NewClient(
		WithTransport(mock),
		WithBackoff(NewConstantBackoff(time.Millisecond)),
	)

	// Case 1: req has an active context, but passed ctx is canceled
	// The passed ctx must take precedence and cause an immediate cancellation error.
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	healthyReq, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/ctx1", nil)
	_, err := client.Do(canceledCtx, healthyReq)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled because passed ctx was canceled, got %v", err)
	}

	// Case 2: req has a canceled context, but passed ctx is healthy
	// The passed healthy ctx must replace req's context, and the request must succeed.
	reqWithCanceledCtx, _ := http.NewRequestWithContext(canceledCtx, http.MethodGet, "http://example.com/ctx2", nil)
	resp, err := client.Do(context.Background(), reqWithCanceledCtx)
	if err != nil {
		t.Fatalf("expected request to succeed using passed context, got error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestClient_PerAttemptTimeout(t *testing.T) {
	var attempts int32
	mock := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			// Check if context times out
			<-req.Context().Done()
			return nil, req.Context().Err()
		}
		return stringResponse(http.StatusOK, "quick"), nil
	})

	client := NewClient(
		WithMaxRetries(2),
		WithTransport(mock),
		WithPerAttemptTimeout(30*time.Millisecond),
		WithBackoff(NewConstantBackoff(time.Millisecond)),
	)

	resp, err := client.Get(context.Background(), "http://example.com/per-attempt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if atomic.LoadInt32(&attempts) < 2 {
		t.Errorf("expected at least 2 attempts due to per-attempt timeout, got %d", attempts)
	}
}

func TestClient_ConvenienceMethods(t *testing.T) {
	mock := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.Method {
		case http.MethodHead:
			resp := stringResponse(http.StatusOK, "")
			resp.Header.Set("X-Custom", "head-ok")
			return resp, nil
		case http.MethodPost:
			b, _ := io.ReadAll(r.Body)
			if r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
				vals, _ := url.ParseQuery(string(b))
				return stringResponse(http.StatusOK, vals.Get("key")), nil
			}
			return stringResponse(http.StatusOK, string(b)), nil
		default:
			return stringResponse(http.StatusOK, "get-ok"), nil
		}
	})

	client := NewClient(
		WithTransport(mock),
		WithBackoff(NewConstantBackoff(time.Millisecond)),
	)

	ctx := context.Background()

	// HEAD
	headResp, err := client.Head(ctx, "http://example.com/head")
	if err != nil || headResp.Header.Get("X-Custom") != "head-ok" {
		t.Errorf("Head failed: %v", err)
	}

	// POST
	postResp, err := client.Post(ctx, "http://example.com/post", "text/plain", bytes.NewReader([]byte("post-body")))
	if err != nil {
		t.Fatalf("Post failed: %v", err)
	}
	postData, _ := io.ReadAll(postResp.Body)
	postResp.Body.Close()
	if string(postData) != "post-body" {
		t.Errorf("Post got %s, want post-body", string(postData))
	}

	// POST FORM
	formVals := url.Values{"key": []string{"val123"}}
	formResp, err := client.PostForm(ctx, "http://example.com/form", formVals)
	if err != nil {
		t.Fatalf("PostForm failed: %v", err)
	}
	formData, _ := io.ReadAll(formResp.Body)
	formResp.Body.Close()
	if string(formData) != "val123" {
		t.Errorf("PostForm got %s, want val123", string(formData))
	}
}

func TestClient_NilRequest(t *testing.T) {
	client := NewClient()
	_, err := client.Do(context.Background(), nil)
	if err == nil {
		t.Fatalf("expected error for nil request")
	}
}

func TestClient_CustomPolicyFatalError(t *testing.T) {
	mock := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusInternalServerError, "err"), nil
	})

	customErr := errors.New("fatal policy error")
	client := NewClient(
		WithMaxRetries(3),
		WithTransport(mock),
		WithRetryPolicy(func(ctx context.Context, resp *http.Response, err error) (bool, error) {
			return false, customErr
		}),
	)

	_, err := client.Get(context.Background(), "http://example.com/fatal")
	if !errors.Is(err, customErr) {
		t.Errorf("expected fatal error %v, got %v", customErr, err)
	}
}
