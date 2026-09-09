package httpr

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestLoggers(t *testing.T) {
	// NoopLogger
	var noop NoopLogger
	noop.Printf("this is discarded %s", "test")

	// StdLogger
	var buf bytes.Buffer
	stdLog := NewStdLogger(&buf, "[test] ", 0)
	stdLog.Printf("hello %s", "world")
	if !strings.Contains(buf.String(), "[test] hello world") {
		t.Errorf("unexpected log output: %q", buf.String())
	}

	// SlogAdapter
	var slogBuf bytes.Buffer
	jsonHandler := slog.NewTextHandler(&slogBuf, &slog.HandlerOptions{Level: slog.LevelInfo})
	slogger := slog.New(jsonHandler)
	slogAdapter := NewSlogLogger(slogger, slog.LevelInfo)
	slogAdapter.Printf("slog message: %d", 42)
	if !strings.Contains(slogBuf.String(), "slog message: 42") {
		t.Errorf("unexpected slog output: %q", slogBuf.String())
	}

	// SlogAdapter with default logger
	defaultSlog := NewSlogLogger(nil, slog.LevelDebug)
	if defaultSlog == nil {
		t.Errorf("expected non-nil slog adapter")
	}
}
