package resilience

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestBreaker() (*Breaker, *clock) {
	c := &clock{t: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}
	return New("order", WithThreshold(5), WithOpenTimeout(10*time.Second), WithClock(c.now)), c
}

var unavailable = status.Error(codes.Unavailable, "connection refused")

func call(b *Breaker, result error) error {
	if err := b.Allow(); err != nil {
		return err
	}
	b.Done(result)
	return result
}

func TestBreakerOpensAfterConsecutiveFailures(t *testing.T) {
	b, _ := newTestBreaker()
	for i := 0; i < 4; i++ {
		_ = call(b, unavailable)
	}
	if b.State() != StateClosed {
		t.Fatalf("4 failures must not open the breaker, state=%s", b.State())
	}
	_ = call(b, unavailable)
	if b.State() != StateOpen {
		t.Fatalf("5th failure must open the breaker, state=%s", b.State())
	}
	if err := b.Allow(); status.Code(err) != codes.Unavailable {
		t.Fatalf("open breaker must reject calls with Unavailable, got %v", err)
	}
}

func TestSuccessResetsFailureCounter(t *testing.T) {
	b, _ := newTestBreaker()
	for i := 0; i < 4; i++ {
		_ = call(b, unavailable)
	}
	_ = call(b, nil)
	for i := 0; i < 4; i++ {
		_ = call(b, unavailable)
	}
	if b.State() != StateClosed {
		t.Fatalf("failures are counted only in a row, state=%s", b.State())
	}
}

func TestBusinessErrorsDoNotOpenBreaker(t *testing.T) {
	b, _ := newTestBreaker()
	for i := 0; i < 20; i++ {
		_ = call(b, status.Error(codes.NotFound, "заказ не найден"))
		_ = call(b, status.Error(codes.FailedPrecondition, "нельзя"))
	}
	if b.State() != StateClosed {
		t.Fatalf("business errors must not open the breaker")
	}
	if !IsFailure(status.Error(codes.DeadlineExceeded, "slow")) || IsFailure(nil) || IsFailure(errors.New("x")) {
		t.Fatal("IsFailure classification is wrong")
	}
}

func TestHalfOpenProbe(t *testing.T) {
	b, c := newTestBreaker()
	for i := 0; i < 5; i++ {
		_ = call(b, unavailable)
	}
	c.advance(9 * time.Second)
	if err := b.Allow(); err == nil {
		t.Fatal("breaker must stay open until the timeout expires")
	}
	c.advance(time.Second)
	if b.State() != StateHalfOpen {
		t.Fatalf("after timeout state must be half-open, got %s", b.State())
	}
	if err := b.Allow(); err != nil {
		t.Fatalf("first call after timeout is a probe and must pass: %v", err)
	}
	if err := b.Allow(); err == nil {
		t.Fatal("only one probe is allowed in half-open state")
	}
	b.Done(nil)
	if b.State() != StateClosed {
		t.Fatalf("successful probe must close the breaker, got %s", b.State())
	}
	if err := b.Allow(); err != nil {
		t.Fatal("closed breaker must allow calls")
	}
	b.Done(nil)
}

func TestFailedProbeReopens(t *testing.T) {
	b, c := newTestBreaker()
	for i := 0; i < 5; i++ {
		_ = call(b, unavailable)
	}
	c.advance(10 * time.Second)
	if err := call(b, unavailable); status.Code(err) != codes.Unavailable {
		t.Fatalf("probe must be executed, got %v", err)
	}
	if b.State() != StateOpen {
		t.Fatalf("failed probe must open the breaker again, got %s", b.State())
	}
	c.advance(5 * time.Second)
	if err := b.Allow(); err == nil {
		t.Fatal("open timeout must restart after a failed probe")
	}
}

func TestInterceptor(t *testing.T) {
	b, _ := newTestBreaker()
	ic := b.UnaryClientInterceptor()
	calls := 0
	failing := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		calls++
		return unavailable
	}
	for i := 0; i < 8; i++ {
		_ = ic(context.Background(), "/order.v1.OrderService/GetOrder", nil, nil, nil, failing)
	}
	if calls != 5 {
		t.Fatalf("after opening the breaker the invoker must not be called, calls=%d", calls)
	}
	// Отмена запроса клиентом не считается отказом сервиса.
	b2, _ := newTestBreaker()
	ic2 := b2.UnaryClientInterceptor()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		return status.Error(codes.Unavailable, "canceled")
	}
	for i := 0; i < 10; i++ {
		_ = ic2(ctx, "/x", nil, nil, nil, canceled)
	}
	if b2.State() != StateClosed {
		t.Fatal("client-side cancellation must not open the breaker")
	}
}
