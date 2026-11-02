package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thaperfectcell/food-delivery/internal/payment/domain"
	"github.com/thaperfectcell/food-delivery/pkg/apperr"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
	"github.com/thaperfectcell/food-delivery/pkg/events"
	"github.com/thaperfectcell/food-delivery/pkg/logger"
)

// ------------------------------------------------------------ тестовые двойники

type memRepo struct {
	mu        sync.Mutex
	byOrder   map[string]*domain.Payment
	processed map[string]bool
	outbox    []events.Envelope
}

func newMemRepo() *memRepo {
	return &memRepo{byOrder: map[string]*domain.Payment{}, processed: map[string]bool{}}
}

func (r *memRepo) GetByOrder(_ context.Context, orderID string) (*domain.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.byOrder[orderID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	c := *p
	return &c, nil
}

func (r *memRepo) List(context.Context, int, int) ([]*domain.Payment, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.Payment
	for _, p := range r.byOrder {
		c := *p
		out = append(out, &c)
	}
	return out, len(out), nil
}

func (r *memRepo) IsProcessed(_ context.Context, eventID string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.processed[eventID], nil
}

func (r *memRepo) CreateOnce(_ context.Context, eventID, _ string, p *domain.Payment, evts []events.Envelope) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.processed[eventID] {
		return false, nil
	}
	if _, exists := r.byOrder[p.OrderID]; exists {
		return false, domain.ErrPaymentExists
	}
	c := *p
	r.byOrder[p.OrderID] = &c
	r.processed[eventID] = true
	r.outbox = append(r.outbox, evts...)
	return true, nil
}

func (r *memRepo) UpdateOnce(_ context.Context, eventID, _, orderID string, fn MutateFunc) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.processed[eventID] {
		return false, nil
	}
	p, ok := r.byOrder[orderID]
	if !ok {
		return false, domain.ErrNotFound
	}
	c := *p
	evts, err := fn(&c)
	if err != nil {
		return false, err
	}
	r.byOrder[orderID] = &c
	r.processed[eventID] = true
	r.outbox = append(r.outbox, evts...)
	return true, nil
}

func (r *memRepo) events() []events.Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]events.Envelope(nil), r.outbox...)
}

type fakeProvider struct {
	limit   int64
	fail    error
	charges []string
	refunds []string
}

func (p *fakeProvider) Charge(_ context.Context, key string, amount int64) (domain.ChargeResult, error) {
	if p.fail != nil {
		return domain.ChargeResult{}, p.fail
	}
	p.charges = append(p.charges, key)
	if amount > p.limit {
		return domain.ChargeResult{Approved: false, DeclineReason: "превышен лимит суммы платежа"}, nil
	}
	return domain.ChargeResult{Approved: true, ProviderRef: "SIM-" + key[:8]}, nil
}

func (p *fakeProvider) Refund(_ context.Context, ref string, _ int64) error {
	if p.fail != nil {
		return p.fail
	}
	p.refunds = append(p.refunds, ref)
	return nil
}

func newService() (*Service, *memRepo, *fakeProvider) {
	repo := newMemRepo()
	prov := &fakeProvider{limit: 1_000_000}
	svc := New(repo, prov, logger.Discard())
	svc.now = func() time.Time { return time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC) }
	return svc, repo, prov
}

func order(total int64) events.OrderPayload {
	return events.OrderPayload{OrderID: uuid.NewString(), CustomerID: uuid.NewString(), Total: total, Status: "created"}
}

func lastType(t *testing.T, repo *memRepo) string {
	t.Helper()
	evts := repo.events()
	if len(evts) == 0 {
		t.Fatal("no events published")
	}
	return evts[len(evts)-1].Type
}

// ------------------------------------------------------------ тесты

func TestOrderCreatedChargesAndPublishesSuccess(t *testing.T) {
	ctx := context.Background()
	svc, repo, prov := newService()
	o := order(1297_00)

	if err := svc.HandleOrderCreated(ctx, uuid.NewString(), o); err != nil {
		t.Fatal(err)
	}
	p, err := repo.GetByOrder(ctx, o.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != domain.StatusSucceeded || p.Amount != 1297_00 || p.ProviderRef == "" {
		t.Fatalf("unexpected payment %+v", p)
	}
	if len(prov.charges) != 1 || prov.charges[0] != o.OrderID {
		t.Fatalf("provider must be charged once with order id as idempotency key: %v", prov.charges)
	}
	evt := repo.events()[0]
	var payload events.PaymentPayload
	if err := evt.Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if evt.Type != events.PaymentSucceeded || payload.OrderID != o.OrderID || payload.Amount != 1297_00 {
		t.Fatalf("unexpected event %s %+v", evt.Type, payload)
	}
}

func TestOrderCreatedOverLimitFails(t *testing.T) {
	ctx := context.Background()
	svc, repo, _ := newService()
	o := order(1_500_000)
	if err := svc.HandleOrderCreated(ctx, uuid.NewString(), o); err != nil {
		t.Fatal(err)
	}
	p, _ := repo.GetByOrder(ctx, o.OrderID)
	if p.Status != domain.StatusFailed || p.FailureReason != "превышен лимит суммы платежа" {
		t.Fatalf("unexpected payment %+v", p)
	}
	if lastType(t, repo) != events.PaymentFailed {
		t.Fatal("payment.failed must be published")
	}
}

func TestOrderCreatedIsIdempotent(t *testing.T) {
	ctx := context.Background()
	svc, repo, prov := newService()
	o := order(500_00)
	eventID := uuid.NewString()
	for i := 0; i < 3; i++ {
		if err := svc.HandleOrderCreated(ctx, eventID, o); err != nil {
			t.Fatal(err)
		}
	}
	// Другое событие о том же заказе тоже не должно привести к списанию.
	if err := svc.HandleOrderCreated(ctx, uuid.NewString(), o); err != nil {
		t.Fatal(err)
	}
	if len(prov.charges) != 1 || len(repo.events()) != 1 {
		t.Fatalf("charges=%d events=%d, want 1/1", len(prov.charges), len(repo.events()))
	}
}

func TestProviderUnavailableIsRetryable(t *testing.T) {
	svc, repo, prov := newService()
	prov.fail = errors.New("connection refused")
	err := svc.HandleOrderCreated(context.Background(), uuid.NewString(), order(500_00))
	if !apperr.Is(err, apperr.KindUnavailable) {
		t.Fatalf("expected unavailable error, got %v", err)
	}
	if len(repo.events()) != 0 {
		t.Fatal("nothing must be saved when provider is unavailable")
	}
}

func TestInvalidEventIsRejected(t *testing.T) {
	svc, _, _ := newService()
	err := svc.HandleOrderCreated(context.Background(), uuid.NewString(), events.OrderPayload{OrderID: "bad"})
	if !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("expected invalid error, got %v", err)
	}
}

func TestCancelAfterPaymentRefunds(t *testing.T) {
	ctx := context.Background()
	svc, repo, prov := newService()
	o := order(800_00)
	_ = svc.HandleOrderCreated(ctx, uuid.NewString(), o)

	o.Reason = "отклонён рестораном"
	cancelID := uuid.NewString()
	if err := svc.HandleOrderCancelled(ctx, cancelID, o); err != nil {
		t.Fatal(err)
	}
	p, _ := repo.GetByOrder(ctx, o.OrderID)
	if p.Status != domain.StatusRefunded {
		t.Fatalf("payment must be refunded, got %s", p.Status)
	}
	if len(prov.refunds) != 1 {
		t.Fatalf("provider refund must be called once, got %d", len(prov.refunds))
	}
	evt := repo.events()[len(repo.events())-1]
	var payload events.PaymentPayload
	_ = evt.Decode(&payload)
	if evt.Type != events.PaymentRefunded || payload.Reason != "отклонён рестораном" {
		t.Fatalf("unexpected event %s %+v", evt.Type, payload)
	}
	// Повторная доставка того же события не создаёт второй возврат.
	if err := svc.HandleOrderCancelled(ctx, cancelID, o); err != nil {
		t.Fatal(err)
	}
	if n := len(repo.events()); n != 2 {
		t.Fatalf("events = %d, want 2", n)
	}
}

func TestCancelBeforePaymentPreventsLateCharge(t *testing.T) {
	ctx := context.Background()
	svc, repo, prov := newService()
	o := order(800_00)

	if err := svc.HandleOrderCancelled(ctx, uuid.NewString(), o); err != nil {
		t.Fatal(err)
	}
	p, err := repo.GetByOrder(ctx, o.OrderID)
	if err != nil || p.Status != domain.StatusFailed || p.FailureReason != domain.ReasonCancelledBeforePayment {
		t.Fatalf("cancel marker must be stored: %+v %v", p, err)
	}
	if len(repo.events()) != 0 {
		t.Fatal("no payment events expected for cancel-before-pay")
	}
	// Запоздавшее order.created не должно приводить к списанию.
	if err := svc.HandleOrderCreated(ctx, uuid.NewString(), o); err != nil {
		t.Fatal(err)
	}
	if len(prov.charges) != 0 {
		t.Fatal("late order.created must not charge the customer")
	}
}

func TestCancelFailedPaymentDoesNothing(t *testing.T) {
	ctx := context.Background()
	svc, repo, prov := newService()
	o := order(5_000_000) // отклонён по лимиту
	_ = svc.HandleOrderCreated(ctx, uuid.NewString(), o)
	if err := svc.HandleOrderCancelled(ctx, uuid.NewString(), o); err != nil {
		t.Fatal(err)
	}
	if len(prov.refunds) != 0 || len(repo.events()) != 1 {
		t.Fatalf("no refund expected: refunds=%d events=%d", len(prov.refunds), len(repo.events()))
	}
}

func TestGetPaymentAccess(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService()
	o := order(500_00)
	_ = svc.HandleOrderCreated(ctx, uuid.NewString(), o)

	owner := auth.Identity{UserID: o.CustomerID, Role: auth.RoleCustomer}
	if _, err := svc.GetPaymentByOrder(ctx, owner, o.OrderID); err != nil {
		t.Fatalf("owner must see payment: %v", err)
	}
	stranger := auth.Identity{UserID: uuid.NewString(), Role: auth.RoleCustomer}
	if _, err := svc.GetPaymentByOrder(ctx, stranger, o.OrderID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("stranger must be forbidden, got %v", err)
	}
	if _, err := svc.GetPaymentByOrder(ctx, auth.System, o.OrderID); err != nil {
		t.Fatalf("system must see payment: %v", err)
	}
	if _, err := svc.GetPaymentByOrder(ctx, auth.System, uuid.NewString()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown order must be not found, got %v", err)
	}
	if _, _, err := svc.ListPayments(ctx, owner, 10, 0); !apperr.Is(err, apperr.KindForbidden) {
		t.Fatalf("customer must not list payments, got %v", err)
	}
	list, total, err := svc.ListPayments(ctx, auth.Identity{UserID: "a", Role: auth.RoleAdmin}, 0, -1)
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("admin list: %v %d %d", err, total, len(list))
	}
}
