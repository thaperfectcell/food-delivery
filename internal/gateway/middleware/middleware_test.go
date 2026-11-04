package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thaperfectcell/food-delivery/pkg/auth"
	"github.com/thaperfectcell/food-delivery/pkg/logger"
	"github.com/thaperfectcell/food-delivery/pkg/requestid"
)

// ------------------------------------------------------------ rate limiter

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newLimiter(rps float64, burst int) (*RateLimiter, *fakeClock) {
	c := &fakeClock{t: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}
	l := NewRateLimiter(rps, burst)
	l.now = c.now
	return l, c
}

func TestRateLimiterBurstAndRefill(t *testing.T) {
	l, c := newLimiter(2, 3)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("1.1.1.1"); !ok {
			t.Fatalf("request %d within burst must pass", i+1)
		}
	}
	ok, wait := l.Allow("1.1.1.1")
	if ok {
		t.Fatal("request over burst must be rejected")
	}
	if wait <= 0 || wait > 500*time.Millisecond {
		t.Fatalf("retry-after must be about 1/rate, got %v", wait)
	}
	c.advance(500 * time.Millisecond) // +1 токен при 2 rps
	if ok, _ := l.Allow("1.1.1.1"); !ok {
		t.Fatal("token must be refilled after 1/rate seconds")
	}
	if ok, _ := l.Allow("1.1.1.1"); ok {
		t.Fatal("only one token must have been refilled")
	}
	c.advance(time.Hour)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("1.1.1.1"); !ok {
			t.Fatal("bucket must not exceed burst after long idle")
		}
	}
	if ok, _ := l.Allow("1.1.1.1"); ok {
		t.Fatal("bucket capacity must be limited by burst")
	}
}

func TestRateLimiterKeysAreIndependentAndCleanedUp(t *testing.T) {
	l, c := newLimiter(1, 1)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("a must pass")
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("b must not be affected by a")
	}
	if l.Len() != 2 {
		t.Fatalf("expected 2 buckets, got %d", l.Len())
	}
	c.advance(10 * time.Minute)
	_, _ = l.Allow("c")
	if l.Len() != 1 {
		t.Fatalf("idle buckets must be removed, got %d", l.Len())
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	l, _ := newLimiter(1, 2)
	h := RateLimit(l, func(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/api/") })(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	do := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "10.0.0.1:5555"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	do("/api/v1/restaurants")
	do("/api/v1/restaurants")
	rec := do("/api/v1/restaurants")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != "rate_limited" {
		t.Fatalf("unexpected body %s", rec.Body.String())
	}
	if rec := do("/index.html"); rec.Code != http.StatusOK {
		t.Fatal("static files are not rate limited")
	}
}

// ------------------------------------------------------------ аутентификация и RBAC

func authChain(tokens *auth.TokenManager, roles ...auth.Role) http.Handler {
	final := func(w http.ResponseWriter, r *http.Request) {
		id, _ := Identity(r)
		w.Write([]byte(id.UserID + "|" + string(id.Role)))
	}
	return Authenticate(tokens)(RequireRole(roles...)(final))
}

func TestAuthenticateAndRequireRole(t *testing.T) {
	tokens := auth.NewTokenManager("test-secret", time.Hour)
	courier, _, _ := tokens.Issue("u-courier", auth.RoleCourier, "Иван")
	customer, _, _ := tokens.Issue("u-customer", auth.RoleCustomer, "Алексей")

	cases := []struct {
		name   string
		header string
		roles  []auth.Role
		status int
		body   string
	}{
		{"anonymous", "", []auth.Role{auth.RoleCustomer}, http.StatusUnauthorized, ""},
		{"garbage token", "Bearer abc.def.ghi", nil, http.StatusUnauthorized, ""},
		{"wrong scheme", "Basic dXNlcjpwYXNz", nil, http.StatusUnauthorized, ""},
		{"wrong role", "Bearer " + courier, []auth.Role{auth.RoleCustomer}, http.StatusForbidden, ""},
		{"right role", "Bearer " + customer, []auth.Role{auth.RoleCustomer, auth.RoleAdmin}, http.StatusOK, "u-customer|customer"},
		{"any role", "bearer " + courier, nil, http.StatusOK, "u-courier|courier"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/orders", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			authChain(tokens, tc.roles...).ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.status, rec.Body.String())
			}
			if tc.body != "" && rec.Body.String() != tc.body {
				t.Fatalf("body = %q, want %q", rec.Body.String(), tc.body)
			}
		})
	}
}

func TestAnonymousPassesPublicRoutes(t *testing.T) {
	tokens := auth.NewTokenManager("test-secret", time.Hour)
	h := Authenticate(tokens)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := Identity(r); ok {
			t.Error("anonymous request must not have identity")
		}
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/restaurants", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("public route must be reachable anonymously, got %d", rec.Code)
	}
}

// ------------------------------------------------------------ прочее

func TestRequestIDAndCORS(t *testing.T) {
	var seen string
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = requestid.From(r.Context())
	}), RequestID, CORS([]string{"http://localhost:3000"}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/restaurants", nil)
	req.Header.Set("X-Request-ID", "abc-123")
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if seen != "abc-123" || rec.Header().Get("X-Request-ID") != "abc-123" {
		t.Fatalf("request id must be propagated, got %q", seen)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Fatal("allowed origin must be echoed")
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "bad id with spaces")
	req.Header.Set("Origin", "http://evil.example")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if seen == "bad id with spaces" || seen == "" {
		t.Fatalf("invalid request id must be replaced, got %q", seen)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("unknown origin must not be allowed")
	}

	pre := httptest.NewRequest(http.MethodOptions, "/api/v1/orders", nil)
	pre.Header.Set("Origin", "http://localhost:3000")
	pre.Header.Set("Access-Control-Request-Method", "POST")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, pre)
	if rec.Code != http.StatusNoContent || !strings.Contains(rec.Header().Get("Access-Control-Allow-Headers"), "Idempotency-Key") {
		t.Fatalf("preflight must be answered with 204, got %d", rec.Code)
	}
}

func TestRecoverAndAccessLog(t *testing.T) {
	log := logger.Discard()
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}), RequestID, AccessLog(log), Recover(log))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/x", nil))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "request_id") {
		t.Fatalf("panic must be converted to 500 JSON, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestResponseRecorderSupportsFlushAndUnwrap(t *testing.T) {
	inner := httptest.NewRecorder()
	rec := &responseRecorder{ResponseWriter: inner, status: http.StatusOK}
	var w http.ResponseWriter = rec
	if _, ok := w.(http.Flusher); !ok {
		t.Fatal("recorder must implement http.Flusher")
	}
	if rec.Unwrap() != inner {
		t.Fatal("Unwrap must return the original writer")
	}
	if err := http.NewResponseController(w).Flush(); err != nil {
		t.Fatalf("ResponseController.Flush: %v", err)
	}
	if !inner.Flushed {
		t.Fatal("flush must reach the underlying writer")
	}
}
