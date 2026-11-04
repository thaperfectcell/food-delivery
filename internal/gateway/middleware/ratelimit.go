package middleware

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/thaperfectcell/food-delivery/internal/gateway/httpx"
)

// RateLimiter — ограничитель частоты запросов по алгоритму «ведро с токенами»
// (token bucket) с отдельным ведром на каждый ключ (IP-адрес клиента).
// Ведро вмещает burst токенов и пополняется со скоростью rate токенов в
// секунду; каждый запрос расходует один токен. Это допускает короткие
// всплески нагрузки и ограничивает среднюю интенсивность.
type RateLimiter struct {
	rate    float64
	burst   float64
	idleTTL time.Duration
	now     func() time.Time

	mu          sync.Mutex
	buckets     map[string]*bucket
	lastCleanup time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewRateLimiter создаёт ограничитель: rps — средняя скорость, burst — размер всплеска.
func NewRateLimiter(rps float64, burst int) *RateLimiter {
	if rps <= 0 {
		rps = 1
	}
	if burst < 1 {
		burst = 1
	}
	return &RateLimiter{
		rate:    rps,
		burst:   float64(burst),
		idleTTL: 5 * time.Minute,
		now:     time.Now,
		buckets: map[string]*bucket{},
	}
}

// Allow расходует токен ключа key. Если токенов нет, возвращает false и
// время, через которое появится следующий токен.
func (l *RateLimiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.cleanup(now)

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rate)
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	return false, wait
}

// cleanup периодически удаляет вёдра неактивных клиентов (они всё равно
// полные), чтобы память не росла неограниченно.
func (l *RateLimiter) cleanup(now time.Time) {
	if now.Sub(l.lastCleanup) < time.Minute {
		return
	}
	l.lastCleanup = now
	for k, b := range l.buckets {
		if now.Sub(b.last) > l.idleTTL {
			delete(l.buckets, k)
		}
	}
}

// Len — число отслеживаемых ключей (для тестов и метрик).
func (l *RateLimiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// RateLimit применяет ограничитель к запросам, для которых applies(r) == true.
// При превышении лимита отвечает 429 Too Many Requests с заголовком Retry-After.
func RateLimit(l *RateLimiter, applies func(*http.Request) bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if applies != nil && !applies(r) {
				next.ServeHTTP(w, r)
				return
			}
			ok, wait := l.Allow(ClientIP(r))
			if !ok {
				secs := int(math.Ceil(wait.Seconds()))
				if secs < 1 {
					secs = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(secs))
				httpx.WriteError(w, r, http.StatusTooManyRequests, "rate_limited", "слишком много запросов, повторите позже")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
