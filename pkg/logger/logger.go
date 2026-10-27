// Package logger настраивает структурированное журналирование (log/slog, JSON).
// Каждая запись содержит имя сервиса и, если он есть в контексте, request_id.
package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/thaperfectcell/food-delivery/pkg/requestid"
)

// New создаёт JSON-логгер для сервиса с заданным уровнем (debug|info|warn|error).
func New(service, level string) *slog.Logger {
	return NewWithWriter(os.Stdout, service, level)
}

// NewWithWriter — то же, что New, но с произвольным приёмником (для тестов).
func NewWithWriter(w io.Writer, service, level string) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: parseLevel(level)})
	return slog.New(contextHandler{Handler: h}).With(slog.String("service", service))
}

// Discard возвращает логгер, который ничего не пишет (удобно в unit-тестах).
func Discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// contextHandler добавляет в запись атрибуты из контекста.
type contextHandler struct {
	slog.Handler
}

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := requestid.From(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{Handler: h.Handler.WithGroup(name)}
}
