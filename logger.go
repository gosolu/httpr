package httpr

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
)

// Logger is a generic logging interface used by httpr.
// Both standard log.Logger and custom loggers can easily satisfy this interface.
type Logger interface {
	Printf(format string, v ...any)
}

// NoopLogger implements Logger and discards all log messages.
type NoopLogger struct{}

func (NoopLogger) Printf(format string, v ...any) {}

// StdLogger wraps standard log.Logger.
type StdLogger struct {
	logger *log.Logger
}

// NewStdLogger returns a Logger wrapping the standard library log.Logger.
func NewStdLogger(w io.Writer, prefix string, flag int) Logger {
	return &StdLogger{
		logger: log.New(w, prefix, flag),
	}
}

func (l *StdLogger) Printf(format string, v ...any) {
	if l.logger != nil {
		l.logger.Printf(format, v...)
	}
}

// SlogAdapter adapts *slog.Logger to the Logger interface.
type SlogAdapter struct {
	logger *slog.Logger
	level  slog.Level
}

// NewSlogLogger returns a Logger wrapping a *slog.Logger at the specified log level.
func NewSlogLogger(logger *slog.Logger, level slog.Level) Logger {
	if logger == nil {
		logger = slog.Default()
	}
	return &SlogAdapter{
		logger: logger,
		level:  level,
	}
}

func (s *SlogAdapter) Printf(format string, v ...any) {
	if s.logger != nil {
		s.logger.Log(context.Background(), s.level, fmt.Sprintf(format, v...))
	}
}
