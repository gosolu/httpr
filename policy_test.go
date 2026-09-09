package httpr

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
)

func TestDefaultRetryPolicy_StatusCodes(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		code      int
		wantRetry bool
	}{
		{code: http.StatusOK, wantRetry: false},
		{code: http.StatusCreated, wantRetry: false},
		{code: http.StatusBadRequest, wantRetry: false},
		{code: http.StatusUnauthorized, wantRetry: false},
		{code: http.StatusForbidden, wantRetry: false},
		{code: http.StatusNotFound, wantRetry: false},
		{code: http.StatusRequestTimeout, wantRetry: true}, // 408
		{code: http.StatusTooManyRequests, wantRetry: true}, // 429
		{code: http.StatusInternalServerError, wantRetry: true}, // 500
		{code: http.StatusNotImplemented, wantRetry: false}, // 501
		{code: http.StatusBadGateway, wantRetry: true}, // 502
		{code: http.StatusServiceUnavailable, wantRetry: true}, // 503
		{code: http.StatusGatewayTimeout, wantRetry: true}, // 504
	}

	for _, tt := range tests {
		resp := &http.Response{StatusCode: tt.code}
		retry, err := DefaultRetryPolicy(ctx, resp, nil)
		if err != nil {
			t.Errorf("code %d: unexpected error %v", tt.code, err)
		}
		if retry != tt.wantRetry {
			t.Errorf("code %d: got retry=%v, want %v", tt.code, retry, tt.wantRetry)
		}
	}
}

func TestDefaultRetryPolicy_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resp := &http.Response{StatusCode: http.StatusInternalServerError}
	retry, err := DefaultRetryPolicy(ctx, resp, nil)
	if retry {
		t.Errorf("expected no retry when context is canceled")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled error, got %v", err)
	}

	// Also when err is context.Canceled
	retry, _ = DefaultRetryPolicy(context.Background(), nil, context.Canceled)
	if retry {
		t.Errorf("expected no retry when err is context.Canceled")
	}
}

func TestDefaultRetryPolicy_NetworkErrors(t *testing.T) {
	ctx := context.Background()

	// Transient network error
	opErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	retry, err := DefaultRetryPolicy(ctx, nil, opErr)
	if err != nil || !retry {
		t.Errorf("expected retry on net.OpError, got retry=%v, err=%v", retry, err)
	}

	// Non-transient URL parse error
	urlErr := &url.Error{Op: "parse", URL: "http://[::1]:namedport", Err: errors.New("invalid port")}
	retry, err = DefaultRetryPolicy(ctx, nil, urlErr)
	if err != nil || retry {
		t.Errorf("expected no retry on URL parse error, got retry=%v, err=%v", retry, err)
	}
}

func TestRetryOnStatusCodes(t *testing.T) {
	policy := RetryOnStatusCodes(http.StatusConflict, http.StatusTeapot)

	ctx := context.Background()
	retry, _ := policy(ctx, &http.Response{StatusCode: http.StatusConflict}, nil)
	if !retry {
		t.Errorf("expected retry on 409 Conflict")
	}

	retry, _ = policy(ctx, &http.Response{StatusCode: http.StatusTeapot}, nil)
	if !retry {
		t.Errorf("expected retry on 418 Teapot")
	}

	retry, _ = policy(ctx, &http.Response{StatusCode: http.StatusInternalServerError}, nil)
	if retry {
		t.Errorf("did not expect retry on 500")
	}
}

func TestCombinePolicies(t *testing.T) {
	ctx := context.Background()

	p1 := RetryOnStatusCodes(http.StatusTeapot)
	p2 := RetryOnStatusCodes(http.StatusBadGateway)
	combined := CombinePolicies(p1, p2)

	retry, _ := combined(ctx, &http.Response{StatusCode: http.StatusTeapot}, nil)
	if !retry {
		t.Errorf("expected combined to retry on 418")
	}

	retry, _ = combined(ctx, &http.Response{StatusCode: http.StatusBadGateway}, nil)
	if !retry {
		t.Errorf("expected combined to retry on 502")
	}

	retry, _ = combined(ctx, &http.Response{StatusCode: http.StatusOK}, nil)
	if retry {
		t.Errorf("expected combined not to retry on 200")
	}
}

func TestNeverRetry(t *testing.T) {
	policy := NeverRetry()
	ctx := context.Background()

	retry, err := policy(ctx, &http.Response{StatusCode: http.StatusInternalServerError}, nil)
	if retry || err != nil {
		t.Errorf("NeverRetry returned retry=%v, err=%v", retry, err)
	}
}
