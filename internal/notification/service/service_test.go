package service

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thaperfectcell/food-delivery/internal/notification/domain"
	"github.com/thaperfectcell/food-delivery/pkg/apperr"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
	"github.com/thaperfectcell/food-delivery/pkg/events"
	"github.com/thaperfectcell/food-delivery/pkg/logger"
)

// ------------------------------------------------------------ тестовые двойники

type memRepo struct {
	mu        sync.Mutex
	items     []domain.Notification
	processed map[string]bool
	fail      error
}

func newMemRepo() *memRepo { return &memRepo{processed: map[string]bool{}} }

func (r *memRepo) SaveOnce(_ context.Context, eventID, _ string, list []domain.Notification) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return false, r.fail
	}
	if r.processed[eventID] {
		return false, nil
	}
	r.processed[eventID] = true
	r.items = append(r.items, list...)
	return true, nil
}

func (r *memRepo) List(_ context.Context, userID string, limit int, unreadOnly bool) ([]domain.Notification, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.Notification
	unread := 0
	for _, n := range r.items {
		if n.UserID != userID {
			continue
		}
		if !n.Read() {
			unread++
		}
		if unreadOnly && n.Read() {
			continue
		}
		out = append(out, n)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, unread, nil
}

func (r *memRepo) MarkRead(_ context.Context, userID string, ids []string, all bool) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	n := 0
	for i := range r.items {
		it := &r.items[i]
		if it.UserID != userID || it.Read() || (!all && !want[it.ID]) {
			continue
		}
		it.ReadAt = time.Now()
		n++
	}
	return n, nil
}

type fakePusher struct {
	mu     sync.Mutex
	pushed []domain.Notification
	fail   error
}

func (p *fakePusher) Push(_ context.Context, n domain.Notification) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail != nil {
		return p.fail
	}
	p.pushed = append(p.pushed, n)
	return nil
}

// ------------------------------------------------------------ окружение

type env struct {
	svc    *Service
	repo   *memRepo
	pusher *fakePusher
	ctx    context.Context
}

func newEnv() *env {
	repo, pusher := newMemRepo(), &fakePusher{}
	svc := New(repo, pusher, logger.Discard())
	clock := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { clock = clock.Add(time.Second); return clock }
	return &env{svc: svc, repo: repo, pusher: pusher, ctx: context.Background()}
}

func paidEvent(t *testing.T, customer, owner string) events.Envelope {
	t.Helper()
	e, err := events.New("order", events.OrderPaid, "trace", events.OrderPayload{
		OrderID: uuid.NewString(), CustomerID: customer, RestaurantOwnerID: owner,
		RestaurantName: "Марио", Total: 1_000_00, ItemsCount: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func user(id string) auth.Identity { return auth.Identity{UserID: id, Role: auth.RoleCustomer} }

// ------------------------------------------------------------ тесты

func TestHandleEventSavesAndPushes(t *testing.T) {
	e := newEnv()
	customer, owner := uuid.NewString(), uuid.NewString()
	evt := paidEvent(t, customer, owner)
	if err := e.svc.HandleEvent(e.ctx, evt); err != nil {
		t.Fatal(err)
	}
	if len(e.repo.items) != 2 || len(e.pusher.pushed) != 2 {
		t.Fatalf("expected 2 saved and pushed notifications, got %d/%d", len(e.repo.items), len(e.pusher.pushed))
	}
	n := e.repo.items[0]
	if n.Type != events.OrderPaid || n.UserID != customer || n.ID == "" || n.CreatedAt.IsZero() || n.OrderID == "" {
		t.Fatalf("unexpected notification %+v", n)
	}
}

func TestDuplicateEventIsNotPushedTwice(t *testing.T) {
	e := newEnv()
	evt := paidEvent(t, uuid.NewString(), uuid.NewString())
	for i := 0; i < 3; i++ {
		if err := e.svc.HandleEvent(e.ctx, evt); err != nil {
			t.Fatal(err)
		}
	}
	if len(e.repo.items) != 2 || len(e.pusher.pushed) != 2 {
		t.Fatalf("duplicates must be ignored: saved=%d pushed=%d", len(e.repo.items), len(e.pusher.pushed))
	}
}

func TestInvalidRecipientIsSkipped(t *testing.T) {
	e := newEnv()
	evt := paidEvent(t, uuid.NewString(), "not-a-uuid")
	if err := e.svc.HandleEvent(e.ctx, evt); err != nil {
		t.Fatal(err)
	}
	if len(e.repo.items) != 1 {
		t.Fatalf("invalid recipient must be skipped, saved=%d", len(e.repo.items))
	}
}

func TestEventWithoutRecipientsTouchesNothing(t *testing.T) {
	e := newEnv()
	evt, _ := events.New("payment", events.PaymentSucceeded, "", events.PaymentPayload{OrderID: uuid.NewString()})
	if err := e.svc.HandleEvent(e.ctx, evt); err != nil {
		t.Fatal(err)
	}
	if len(e.repo.processed) != 0 || len(e.pusher.pushed) != 0 {
		t.Fatal("event without notifications must not be stored")
	}
}

func TestPushFailureDoesNotFailHandling(t *testing.T) {
	e := newEnv()
	e.pusher.fail = errors.New("nats down")
	if err := e.svc.HandleEvent(e.ctx, paidEvent(t, uuid.NewString(), uuid.NewString())); err != nil {
		t.Fatalf("push failure must not fail handling: %v", err)
	}
	if len(e.repo.items) != 2 {
		t.Fatal("notifications must be kept in history")
	}
}

func TestStorageFailureIsRetried(t *testing.T) {
	e := newEnv()
	e.repo.fail = errors.New("db down")
	if err := e.svc.HandleEvent(e.ctx, paidEvent(t, uuid.NewString(), uuid.NewString())); err == nil {
		t.Fatal("storage failure must be returned for retry")
	}
	if len(e.pusher.pushed) != 0 {
		t.Fatal("nothing must be pushed before saving")
	}
}

func TestMalformedEventIsPermanent(t *testing.T) {
	e := newEnv()
	evt := events.Envelope{ID: uuid.NewString(), Type: events.OrderPaid, Data: []byte(`[]`)}
	if err := e.svc.HandleEvent(e.ctx, evt); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("expected invalid error, got %v", err)
	}
}

func TestListAndMarkRead(t *testing.T) {
	e := newEnv()
	customer, owner := uuid.NewString(), uuid.NewString()
	for i := 0; i < 3; i++ {
		_ = e.svc.HandleEvent(e.ctx, paidEvent(t, customer, owner))
	}
	list, unread, err := e.svc.ListNotifications(e.ctx, user(customer), 0, false)
	if err != nil || len(list) != 3 || unread != 3 {
		t.Fatalf("list=%d unread=%d err=%v", len(list), unread, err)
	}
	if !list[0].CreatedAt.After(list[2].CreatedAt) {
		t.Fatal("newest notifications must come first")
	}

	// Чужое уведомление не помечается.
	if n, err := e.svc.MarkRead(e.ctx, user(owner), []string{list[0].ID}, false); err != nil || n != 0 {
		t.Fatalf("foreign notification must not be marked: n=%d err=%v", n, err)
	}
	if n, err := e.svc.MarkRead(e.ctx, user(customer), []string{list[0].ID}, false); err != nil || n != 1 {
		t.Fatalf("mark one: n=%d err=%v", n, err)
	}
	if _, unread, _ := e.svc.ListNotifications(e.ctx, user(customer), 0, false); unread != 2 {
		t.Fatalf("unread = %d, want 2", unread)
	}
	only, _, _ := e.svc.ListNotifications(e.ctx, user(customer), 0, true)
	if len(only) != 2 {
		t.Fatalf("unread_only list = %d, want 2", len(only))
	}
	if n, _ := e.svc.MarkRead(e.ctx, user(customer), nil, true); n != 2 {
		t.Fatalf("mark all = %d, want 2", n)
	}
}

func TestMarkReadValidation(t *testing.T) {
	e := newEnv()
	u := user(uuid.NewString())
	if _, err := e.svc.MarkRead(e.ctx, u, nil, false); !errors.Is(err, domain.ErrNothingToMark) {
		t.Fatalf("expected ErrNothingToMark, got %v", err)
	}
	if _, err := e.svc.MarkRead(e.ctx, u, []string{"bad"}, false); !errors.Is(err, domain.ErrInvalidID) {
		t.Fatalf("expected ErrInvalidID, got %v", err)
	}
	if _, _, err := e.svc.ListNotifications(e.ctx, auth.Identity{}, 10, false); !apperr.Is(err, apperr.KindUnauthenticated) {
		t.Fatalf("anonymous must be rejected, got %v", err)
	}
	if _, _, err := e.svc.ListNotifications(e.ctx, auth.System, 10, false); !apperr.Is(err, apperr.KindForbidden) {
		t.Fatalf("system identity has no inbox, got %v", err)
	}
}
