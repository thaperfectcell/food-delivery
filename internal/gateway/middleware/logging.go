package middleware

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// AccessLog журналирует каждый HTTP-запрос: метод, путь, статус, длительность,
// размер ответа, IP клиента и пользователя (если он аутентифицирован).
func AccessLog(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			info := &requestInfo{}
			rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), requestInfoKey{}, info)))

			attrs := []any{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
				slog.Int("bytes", rec.bytes),
				slog.String("ip", ClientIP(r)),
			}
			if info.userID != "" {
				attrs = append(attrs, slog.String("user_id", info.userID), slog.String("role", info.role))
			}
			level := slog.LevelInfo
			switch {
			case rec.status >= 500:
				level = slog.LevelError
			case isProbe(r.URL.Path):
				level = slog.LevelDebug // health-check'и Docker не засоряют журнал
			}
			log.Log(r.Context(), level, "http request", attrs...)
		})
	}
}

func isProbe(path string) bool {
	return path == "/healthz" || path == "/readyz"
}

// ClientIP возвращает IP-адрес клиента из RemoteAddr. Заголовкам
// X-Forwarded-For не доверяем: перед шлюзом нет доверенного прокси, и такие
// заголовки позволили бы обходить ограничение частоты запросов.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
