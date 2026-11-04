// Package middleware — сквозная функциональность HTTP-слоя API Gateway,
// реализованная цепочкой обработчиков (паттерн Chain of Responsibility):
// идентификатор запроса, журналирование, восстановление после паники, CORS,
// ограничение частоты запросов, лимит тела запроса, аутентификация и RBAC.
package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/thaperfectcell/food-delivery/internal/gateway/httpx"
	"github.com/thaperfectcell/food-delivery/pkg/requestid"
)

// Middleware — обёртка обработчика.
type Middleware func(http.Handler) http.Handler

// Chain применяет обёртки так, что первая в списке становится внешней.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// HeaderRequestID — HTTP-заголовок сквозного идентификатора запроса.
const HeaderRequestID = "X-Request-ID"

// RequestID берёт идентификатор из заголовка X-Request-ID (если он корректен)
// или создаёт новый, кладёт его в контекст и возвращает клиенту.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if !validRequestID(id) {
			id = requestid.New()
		}
		w.Header().Set(HeaderRequestID, id)
		next.ServeHTTP(w, r.WithContext(requestid.With(r.Context(), id)))
	})
}

func validRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}

// Recover перехватывает панику обработчика, журналирует её и отвечает 500.
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if p := recover(); p != nil {
					if p == http.ErrAbortHandler {
						panic(p)
					}
					log.ErrorContext(r.Context(), "panic in http handler",
						slog.Any("panic", p), slog.String("path", r.URL.Path),
						slog.String("stack", string(debug.Stack())))
					httpx.WriteError(w, r, http.StatusInternalServerError, "internal", "внутренняя ошибка шлюза")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// BodyLimit ограничивает размер тела запроса.
func BodyLimit(limit int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil && r.Body != http.NoBody {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CORS разрешает обращения к API из браузера с указанных источников
// ("*" — с любых) и отвечает на предварительные (preflight) запросы.
func CORS(origins []string) Middleware {
	allowAny := false
	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		if o == "*" {
			allowAny = true
		}
		allowed[strings.TrimRight(o, "/")] = true
	}
	const (
		methods = "GET, POST, PATCH, DELETE, OPTIONS"
		headers = "Authorization, Content-Type, Idempotency-Key, X-Request-ID"
		expose  = "X-Request-ID, Retry-After"
	)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				switch {
				case allowAny:
					w.Header().Set("Access-Control-Allow-Origin", "*")
				case allowed[origin]:
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Add("Vary", "Origin")
				}
				w.Header().Set("Access-Control-Expose-Headers", expose)
			}
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				w.Header().Set("Access-Control-Allow-Methods", methods)
				w.Header().Set("Access-Control-Allow-Headers", headers)
				w.Header().Set("Access-Control-Max-Age", strconv.Itoa(600))
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ---------------------------------------------------------------- журнал

// requestInfo — изменяемые сведения о запросе, которые заполняют внутренние
// обработчики (например, аутентификация) и читает журнал доступа.
type requestInfo struct {
	userID string
	role   string
}

type requestInfoKey struct{}

func infoFrom(ctx context.Context) *requestInfo {
	info, _ := ctx.Value(requestInfoKey{}).(*requestInfo)
	return info
}

// responseRecorder запоминает статус и размер ответа. Реализует Unwrap и
// Flush, поэтому совместим с http.ResponseController и потоковыми ответами (SSE).
type responseRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (r *responseRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Unwrap возвращает исходный ResponseWriter (для http.ResponseController).
func (r *responseRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Flush отправляет буферизованные данные клиенту.
func (r *responseRecorder) Flush() {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
