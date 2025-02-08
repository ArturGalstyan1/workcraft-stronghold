// logger/logger.go
package logger

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"strings"

	"github.com/getsentry/sentry-go"
	sentryslog "github.com/getsentry/sentry-go/slog"
)

type simpleHandler struct {
	w io.Writer
}

func (h *simpleHandler) Handle(ctx context.Context, r slog.Record) error {
	timeStr := r.Time.Format("2006-01-02 15:04:05")
	level := strings.ToUpper(r.Level.String())

	_, err := fmt.Fprintf(h.w, "%s %s %s\n",
		timeStr,
		level,
		r.Message,
	)
	return err
}

func (h *simpleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h
}

func (h *simpleHandler) WithGroup(name string) slog.Handler {
	return h
}

func (h *simpleHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return true
}

var Log *slog.Logger

func init() {
	handler := &simpleHandler{w: os.Stdout}
	Log = slog.New(handler)

	log.SetFlags(0)
	log.SetPrefix("")
	log.SetOutput(&logWriter{})
}

type logWriter struct{}

func (w *logWriter) Write(p []byte) (n int, err error) {
	msg := strings.TrimSpace(string(p))
	msg = strings.TrimPrefix(msg, "INFO ")

	Log.Info(msg)
	return len(p), nil
}

func Init(dsn string) {
	if dsn == "" {
		return
	}

	if err := sentry.Init(sentry.ClientOptions{
		Dsn:           dsn,
		EnableTracing: false,
	}); err != nil {
		return
	}

	Log = slog.New(sentryslog.Option{
		Level: slog.LevelWarn,
	}.NewSentryHandler())
	Log.Info("Sentry initialized")
}
