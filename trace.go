package httpr

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"
)

// TraceInfo contains timing breakdown and connection metadata for an individual HTTP attempt.
type TraceInfo struct {
	// Attempt is the 1-based sequence number of this attempt (1 for initial attempt, 2 for retry 1, etc.).
	Attempt int

	// DNSDuration is the time spent resolving domain names to IP addresses.
	// Will be 0 if an existing connection was reused or an IP address was dialed directly.
	DNSDuration time.Duration

	// ConnectDuration is the time spent establishing the TCP connection.
	// Will be 0 if an existing connection was reused.
	ConnectDuration time.Duration

	// TLSDuration is the time spent completing the TLS handshake.
	// Will be 0 for plain HTTP or if an existing TLS connection was reused.
	TLSDuration time.Duration

	// WaitDuration is the server response wait time (Time to First Byte / TTFB),
	// measured from request writing completion to receiving the first response byte.
	WaitDuration time.Duration

	// TotalDuration is the total elapsed time for this HTTP attempt.
	TotalDuration time.Duration

	// Reused reports whether an existing keep-alive connection was reused.
	Reused bool

	// WasIdle reports whether the reused connection was taken from an idle connection pool.
	WasIdle bool

	// IdleDuration is the duration the reused connection had been idle in the pool before this attempt.
	IdleDuration time.Duration

	// RemoteAddr is the remote network address dialed or connected to (e.g. "93.184.216.34:443").
	RemoteAddr string
}

// TraceHook is invoked after an HTTP attempt completes, providing the request and detailed trace metrics.
type TraceHook func(req *http.Request, trace TraceInfo)

type traceCollector struct {
	mu           sync.Mutex
	start        time.Time
	dnsStart     time.Time
	connStart    time.Time
	tlsStart     time.Time
	wroteHeaders time.Time
	wroteRequest time.Time
	info         TraceInfo
}

func newTraceCollector(ctx context.Context, attempt int) (context.Context, *traceCollector) {
	tc := &traceCollector{
		start: time.Now(),
		info: TraceInfo{
			Attempt: attempt,
		},
	}

	clientTrace := &httptrace.ClientTrace{
		DNSStart: func(info httptrace.DNSStartInfo) {
			tc.mu.Lock()
			tc.dnsStart = time.Now()
			tc.mu.Unlock()
		},
		DNSDone: func(info httptrace.DNSDoneInfo) {
			tc.mu.Lock()
			if !tc.dnsStart.IsZero() {
				tc.info.DNSDuration = time.Since(tc.dnsStart)
			}
			tc.mu.Unlock()
		},
		ConnectStart: func(network, addr string) {
			tc.mu.Lock()
			tc.connStart = time.Now()
			if tc.info.RemoteAddr == "" {
				tc.info.RemoteAddr = addr
			}
			tc.mu.Unlock()
		},
		ConnectDone: func(network, addr string, err error) {
			tc.mu.Lock()
			if !tc.connStart.IsZero() {
				tc.info.ConnectDuration = time.Since(tc.connStart)
			}
			if tc.info.RemoteAddr == "" {
				tc.info.RemoteAddr = addr
			}
			tc.mu.Unlock()
		},
		TLSHandshakeStart: func() {
			tc.mu.Lock()
			tc.tlsStart = time.Now()
			tc.mu.Unlock()
		},
		TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			tc.mu.Lock()
			if !tc.tlsStart.IsZero() {
				tc.info.TLSDuration = time.Since(tc.tlsStart)
			}
			tc.mu.Unlock()
		},
		GotConn: func(connInfo httptrace.GotConnInfo) {
			tc.mu.Lock()
			tc.info.Reused = connInfo.Reused
			tc.info.WasIdle = connInfo.WasIdle
			tc.info.IdleDuration = connInfo.IdleTime
			if connInfo.Conn != nil && connInfo.Conn.RemoteAddr() != nil {
				tc.info.RemoteAddr = connInfo.Conn.RemoteAddr().String()
			}
			tc.mu.Unlock()
		},
		WroteHeaders: func() {
			tc.mu.Lock()
			if tc.wroteHeaders.IsZero() {
				tc.wroteHeaders = time.Now()
			}
			tc.mu.Unlock()
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			tc.mu.Lock()
			tc.wroteRequest = time.Now()
			tc.mu.Unlock()
		},
		GotFirstResponseByte: func() {
			tc.mu.Lock()
			now := time.Now()
			if !tc.wroteRequest.IsZero() {
				tc.info.WaitDuration = now.Sub(tc.wroteRequest)
			} else if !tc.wroteHeaders.IsZero() {
				tc.info.WaitDuration = now.Sub(tc.wroteHeaders)
			} else {
				tc.info.WaitDuration = now.Sub(tc.start)
			}
			tc.mu.Unlock()
		},
	}

	return httptrace.WithClientTrace(ctx, clientTrace), tc
}

func (tc *traceCollector) finish() TraceInfo {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	tc.info.TotalDuration = time.Since(tc.start)
	return tc.info
}
