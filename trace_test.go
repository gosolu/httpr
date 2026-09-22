package httpr

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWithTrace_RealServer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello trace"))
	}))
	defer ts.Close()

	var (
		mu         sync.Mutex
		traceInfos []TraceInfo
	)

	client := NewClient(
		WithMaxRetries(0),
		WithTrace(func(req *http.Request, trace TraceInfo) {
			mu.Lock()
			traceInfos = append(traceInfos, trace)
			mu.Unlock()
		}),
	)

	ctx := context.Background()
	resp, err := client.Get(ctx, ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	mu.Lock()
	defer mu.Unlock()

	if len(traceInfos) != 1 {
		t.Fatalf("expected 1 traceInfo, got %d", len(traceInfos))
	}

	info := traceInfos[0]
	if info.Attempt != 1 {
		t.Errorf("expected Attempt=1, got %d", info.Attempt)
	}
	if info.TotalDuration <= 0 {
		t.Errorf("expected TotalDuration > 0, got %v", info.TotalDuration)
	}
	if info.WaitDuration <= 0 {
		t.Errorf("expected WaitDuration > 0, got %v", info.WaitDuration)
	}
	if info.RemoteAddr == "" {
		t.Error("expected non-empty RemoteAddr")
	}
}

func TestWithTrace_ConnectionReuse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	var (
		mu         sync.Mutex
		traceInfos []TraceInfo
	)

	client := NewClient(
		WithMaxRetries(0),
		WithTrace(func(req *http.Request, trace TraceInfo) {
			mu.Lock()
			traceInfos = append(traceInfos, trace)
			mu.Unlock()
		}),
	)

	ctx := context.Background()

	// 1st request: new connection
	resp1, err := client.Get(ctx, ts.URL)
	if err != nil {
		t.Fatalf("request 1 error: %v", err)
	}
	drainAndClose(resp1)

	// Small pause so connection returns to idle pool
	time.Sleep(10 * time.Millisecond)

	// 2nd request: should reuse connection
	resp2, err := client.Get(ctx, ts.URL)
	if err != nil {
		t.Fatalf("request 2 error: %v", err)
	}
	drainAndClose(resp2)

	mu.Lock()
	defer mu.Unlock()

	if len(traceInfos) != 2 {
		t.Fatalf("expected 2 traceInfos, got %d", len(traceInfos))
	}

	if traceInfos[0].Reused {
		t.Errorf("request 1: expected Reused=false, got true")
	}
	if !traceInfos[1].Reused {
		t.Errorf("request 2: expected Reused=true, got false")
	}
	if !traceInfos[1].WasIdle {
		t.Errorf("request 2: expected WasIdle=true, got false")
	}
}

func TestWithTrace_RetryAttempts(t *testing.T) {
	var attempts int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("temporary failure"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("recovered"))
	}))
	defer ts.Close()

	var (
		mu         sync.Mutex
		traceInfos []TraceInfo
	)

	client := NewClient(
		WithMaxRetries(2),
		WithBackoff(NewConstantBackoff(time.Millisecond)),
		WithTrace(func(req *http.Request, trace TraceInfo) {
			mu.Lock()
			traceInfos = append(traceInfos, trace)
			mu.Unlock()
		}),
	)

	resp, err := client.Get(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	mu.Lock()
	defer mu.Unlock()

	if len(traceInfos) != 2 {
		t.Fatalf("expected 2 trace calls for 2 attempts, got %d", len(traceInfos))
	}
	if traceInfos[0].Attempt != 1 {
		t.Errorf("expected attempt 1 for first trace, got %d", traceInfos[0].Attempt)
	}
	if traceInfos[1].Attempt != 2 {
		t.Errorf("expected attempt 2 for second trace, got %d", traceInfos[1].Attempt)
	}
}

func TestWithTrace_CoexistsWithContextTrace(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	var (
		outerTraceGotConn atomic.Bool
		httprTraceFired   atomic.Bool
	)

	client := NewClient(
		WithMaxRetries(0),
		WithTrace(func(req *http.Request, trace TraceInfo) {
			httprTraceFired.Store(true)
		}),
	)

	// User attaches their own httptrace to ctx
	userTrace := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			outerTraceGotConn.Store(true)
		},
	}
	ctx := httptrace.WithClientTrace(context.Background(), userTrace)

	resp, err := client.Get(ctx, ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if !outerTraceGotConn.Load() {
		t.Error("expected user's outer ClientTrace.GotConn to be called")
	}
	if !httprTraceFired.Load() {
		t.Error("expected httpr WithTrace hook to be called")
	}
}

func TestWithTrace_TLSServer(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("tls ok"))
	}))
	defer ts.Close()

	var (
		mu        sync.Mutex
		traceInfo TraceInfo
	)

	client := NewClient(
		WithMaxRetries(0),
		WithHTTPClient(ts.Client()), // Uses test certs
		WithTrace(func(req *http.Request, trace TraceInfo) {
			mu.Lock()
			traceInfo = trace
			mu.Unlock()
		}),
	)

	resp, err := client.Get(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	mu.Lock()
	defer mu.Unlock()

	if traceInfo.TLSDuration <= 0 {
		t.Errorf("expected TLSDuration > 0 for HTTPS request, got %v", traceInfo.TLSDuration)
	}
}

func TestWithTrace_DialError(t *testing.T) {
	var (
		mu        sync.Mutex
		traceInfo TraceInfo
	)

	client := NewClient(
		WithMaxRetries(0),
		WithTrace(func(req *http.Request, trace TraceInfo) {
			mu.Lock()
			traceInfo = trace
			mu.Unlock()
		}),
	)

	// Unreachable port
	_, err := client.Get(context.Background(), "http://127.0.0.1:1")
	if err == nil {
		t.Fatal("expected dial error")
	}

	mu.Lock()
	defer mu.Unlock()

	if traceInfo.Attempt != 1 {
		t.Errorf("expected Attempt=1 on dial error, got %d", traceInfo.Attempt)
	}
	if traceInfo.TotalDuration <= 0 {
		t.Errorf("expected TotalDuration > 0 on dial error, got %v", traceInfo.TotalDuration)
	}
}

func TestWithTrace_Concurrent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("concurrent ok"))
	}))
	defer ts.Close()

	var totalTraces int32
	client := NewClient(
		WithMaxRetries(1),
		WithTrace(func(req *http.Request, trace TraceInfo) {
			atomic.AddInt32(&totalTraces, 1)
		}),
	)

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			resp, err := client.Get(context.Background(), ts.URL)
			if err == nil {
				drainAndClose(resp)
			}
		}()
	}

	wg.Wait()

	if atomic.LoadInt32(&totalTraces) != goroutines {
		t.Errorf("expected %d traces, got %d", goroutines, totalTraces)
	}
}

func TestTraceCollector_CallbacksDirect(t *testing.T) {
	// Directly verify each callback path in traceCollector
	ctx, tc := newTraceCollector(context.Background(), 1)
	if ctx == nil || tc == nil {
		t.Fatal("expected non-nil ctx and tc")
	}

	// Trigger callbacks directly to exercise branches
	clientTrace := httptrace.ContextClientTrace(ctx)
	if clientTrace == nil {
		t.Fatal("expected clientTrace on context")
	}

	clientTrace.DNSStart(httptrace.DNSStartInfo{Host: "example.com"})
	clientTrace.DNSDone(httptrace.DNSDoneInfo{})
	clientTrace.ConnectStart("tcp", "1.2.3.4:80")
	clientTrace.ConnectDone("tcp", "1.2.3.4:80", nil)
	clientTrace.TLSHandshakeStart()
	clientTrace.TLSHandshakeDone(tls.ConnectionState{}, nil)
	clientTrace.WroteHeaders()
	clientTrace.WroteRequest(httptrace.WroteRequestInfo{})
	clientTrace.GotFirstResponseByte()

	info := tc.finish()
	if info.RemoteAddr != "1.2.3.4:80" {
		t.Errorf("expected RemoteAddr 1.2.3.4:80, got %s", info.RemoteAddr)
	}
	if info.TotalDuration <= 0 {
		t.Errorf("expected TotalDuration > 0, got %v", info.TotalDuration)
	}
}
