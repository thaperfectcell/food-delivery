package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thaperfectcell/food-delivery/internal/order/domain"
	"github.com/thaperfectcell/food-delivery/pkg/apperr"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
	"github.com/thaperfectcell/food-delivery/pkg/events"
	"github.com/thaperfectcell/food-delivery/pkg/logger"
)

// ------------------------------------------------------------ тестовые двойники

type memRepo struct {
	mu        sync.Mutex
	orders    map[string]*domain.Order
	processed map[string]bool
	outbox    []events.Envelope
}

func newMemRepo() *memRepo {
	return &memRepo{orders: map[string]*domain.Order{}, processed: map[string]bool{}}
}

func clone(o *domain.Order) *domain.Order {
	c := *o
	c.Items = append([]domain.Item(nil), o.Items...)
	c.History = append([]domain.StatusChange(nil), o.History...)
	return &c
}

func (r *memRepo) Create(_ context.Context, o *domain.Order, evts []events.Envelope) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range r.orders {
		if o.IdempotencyKey != "" && x.CustomerID == o.CustomerID && x.IdempotencyKey == o.IdempotencyKey {
			return apperr.Conflict("duplicate")
		}
	}
	o.ClearPending()
	r.orders[o.ID] = clone(o)
	r.outbox = append(r.outbox, evts...)
	return nil
}

func (r *memRepo) Get(_ context.Context, id string) (*domain.Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.orders[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return clone(o), nil
}

func (r *memRepo) GetByIdempotencyKey(_ context.Context, customerID, key string) (*domain.Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, o := range r.orders {
		if o.CustomerID == customerID && o.IdempotencyKey == key {
			return clone(o), nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *memRepo) List(_ context.Context, f domain.ListFilter) ([]*domain.Order, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.Order
	for _, o := range r.orders {
		if f.CustomerID != "" && o.CustomerID != f.CustomerID {
			continue
		}
		if f.RestaurantOwnerID != "" && o.RestaurantOwnerID != f.RestaurantOwnerID {
			continue
		}
		out = append(out, clone(o))
	}
	return out, len(out), nil
}

func (r *memRepo) Update(_ context.Context, id string, fn MutateFunc) (*domain.Order, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.orders[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	c := clone(o)
	evts, err := fn(c)
	if err != nil {
		return nil, err
	}
	c.ClearPending()
	r.orders[id] = c
	r.outbox = append(r.outbox, evts...)
	return clone(c), nil
}

func (r *memRepo) UpdateOnce(ctx context.Context, eventID, _, orderID string, fn MutateFunc) (bool, error) {
	r.mu.Lock()
	if r.processed[eventID] {
		r.mu.Unlock()
		return false, nil
	}
	r.mu.Unlock()
	if _, err := r.Update(ctx, orderID, fn); err != nil {
		return false, err
	}
	r.mu.Lock()
	r.processed[eventID] = true
	r.mu.Unlock()
	return true, nil
}

func (r *memRepo) Stats(context.Context, time.Time) (domain.Stats, error) { return domain.Stats{}, nil }

func (r *memRepo) lastEvent() events.Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.outbox[len(r.outbox)-1]
}

type fakeCatalog struct {
	restaurant domain.Restaurant
	items      []domain.MenuItem
	calls      int
}

func (c *fakeCatalog) GetMenuItems(_ context.Context, restaurantID string, ids []string) (domain.Restaurant, []domain.MenuItem, error) {
	c.calls++
	if restaurantID != c.restaurant.ID {
		return domain.Restaurant{}, nil, apperr.NotFound("ресторан не найден")
	}
	var out []domain.MenuItem
	for _, it := range c.items {
		for _, id := range ids {
			if it.ID == id {
				out = append(out, it)
			}
		}
	}
	return c.restaurant, out, nil
}

// ------------------------------------------------------------ окружение теста

type env struct {
	svc      *Service
	repo     *memRepo
	catalog  *fakeCatalog
	customer auth.Identity
	owner    auth.Identity
	pizza    string
}

func newEnv() *env {
	ownerID, restaurantID, pizza := uuid.NewString(), uuid.NewString(), uuid.NewString()
	cat := &fakeCatalog{
		restaurant: domain.Restaurant{ID: restaurantID, OwnerID: ownerID, Name: "Марио", Address: "ул. Ленина, 1", Active: true},
		items:      []domain.MenuItem{{ID: pizza, Name: "Маргарита", Price: 549_00, Available: true}},
	}
	repo := newMemRepo()
	return &env{
		svc:      New(repo, cat, logger.Discard()),
		repo:     repo,
		catalog:  cat,
		customer: auth.Identity{UserID: uuid.NewString(), Role: auth.RoleCustomer},
		owner:    auth.Identity{UserID: ownerID, Role: auth.RoleRestaurant},
		pizza:    pizza,
	}
}

func (e *env) create(t *testing.T, key string) *domain.Order {
	t.Helper()
	o, err := e.svc.CreateOrder(context.Background(), e.customer, CreateOrderInput{
		RestaurantID:    e.catalog.restaurant.ID,
		Lines:           []domain.Line{{MenuItemID: e.pizza, Quantity: 2}},
		DeliveryAddress: "ул. Пушкина, 10",
		IdempotencyKey:  key,
	})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	return o
}

func payment(orderID string) events.PaymentPayload {
	return events.PaymentPayload{PaymentID: uuid.NewString(), OrderID: orderID, Status: "succeeded"}
}

// ------------------------------------------------------------ тесты

func TestCreateOrderPublishesEvent(t *testing.T) {
	e := newEnv()
	o := e.create(t, "")
	if o.Status != domain.StatusCreated || o.Total != 2*549_00+domain.DeliveryFee {
		t.Fatalf("unexpected order: status=%s total=%d", o.Status, o.Total)
	}
	evt := e.repo.lastEvent()
	if evt.Type != events.OrderCreated {
		t.Fatalf("expected order.created, got %s", evt.Type)
	}
	var p events.OrderPayload
	if err := evt.Decode(&p); err != nil || p.OrderID != o.ID || p.RestaurantOwnerID != e.owner.UserID {
		t.Fatalf("bad payload %+v (%v)", p, err)
	}
}

func TestCreateOrderIsIdempotent(t *testing.T) {
	e := newEnv()
	first := e.create(t, "key-1")
	second := e.create(t, "key-1")
	if first.ID != second.ID {
		t.Fatalf("same idempotency key must return the same order")
	}
	if e.catalog.calls != 1 {
		t.Fatalf("catalog must be called once, got %d", e.catalog.calls)
	}
}

func TestOnlyCustomersCanOrder(t *testing.T) {
	e := newEnv()
	_, err := e.svc.CreateOrder(context.Background(), e.owner, CreateOrderInput{RestaurantID: e.catalog.restaurant.ID})
	if !apperr.Is(err, apperr.KindForbidden) {
		t.Fatalf("expected forbidden, got %v", err)
	}
}

func TestHappyPathSaga(t *testing.T) {
	ctx := context.Background()
	e := newEnv()
	o := e.create(t, "")

	if err := e.svc.HandlePaymentSucceeded(ctx, uuid.NewString(), payment(o.ID)); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.repo.Get(ctx, o.ID); got.Status != domain.StatusPaid {
		t.Fatalf("status = %s, want paid", got.Status)
	}
	if _, err := e.svc.AcceptOrder(ctx, e.owner, o.ID); err != nil {
		t.Fatal(err)
	}
	courier := events.DeliveryPayload{OrderID: o.ID, CourierID: uuid.NewString(), CourierName: "Иван"}
	if err := e.svc.HandleCourierAssigned(ctx, uuid.NewString(), courier); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.MarkReady(ctx, e.owner, o.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.HandlePickedUp(ctx, uuid.NewString(), courier); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.HandleDelivered(ctx, uuid.NewString(), courier); err != nil {
		t.Fatal(err)
	}
	got, _ := e.repo.Get(ctx, o.ID)
	if got.Status != domain.StatusDelivered || got.CourierName != "Иван" {
		t.Fatalf("unexpected final state: %s courier=%q", got.Status, got.CourierName)
	}
	if len(got.History) != 6 {
		t.Fatalf("history must contain 6 records, got %d", len(got.History))
	}
	if e.repo.lastEvent().Type != events.OrderDelivered {
		t.Fatalf("last event = %s", e.repo.lastEvent().Type)
	}
}

func TestDuplicateEventIsIgnored(t *testing.T) {
	ctx := context.Background()
	e := newEnv()
	o := e.create(t, "")
	eventID := uuid.NewString()
	for i := 0; i < 3; i++ {
		if err := e.svc.HandlePaymentSucceeded(ctx, eventID, payment(o.ID)); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := e.repo.Get(ctx, o.ID)
	if len(got.History) != 2 {
		t.Fatalf("event must be applied once, history=%d", len(got.History))
	}
}

func TestPaymentFailedCancelsOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv()
	o := e.create(t, "")
	err := e.svc.HandlePaymentFailed(ctx, uuid.NewString(), events.PaymentPayload{OrderID: o.ID, Reason: "лимит"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := e.repo.Get(ctx, o.ID)
	if got.Status != domain.StatusCancelled || got.CancelReason != "оплата не прошла: лимит" {
		t.Fatalf("unexpected state %s %q", got.Status, got.CancelReason)
	}
	if e.repo.lastEvent().Type != events.OrderCancelled {
		t.Fatal("order.cancelled must be published")
	}
}

func TestCustomerCannotCancelAcceptedOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv()
	o := e.create(t, "")
	_ = e.svc.HandlePaymentSucceeded(ctx, uuid.NewString(), payment(o.ID))
	if _, err := e.svc.AcceptOrder(ctx, e.owner, o.ID); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.CancelOrder(ctx, e.customer, o.ID, "")
	if !apperr.Is(err, apperr.KindPrecondition) {
		t.Fatalf("expected precondition error, got %v", err)
	}
	// А ресторан отклонить принятый заказ может (компенсация — возврат средств).
	got, err := e.svc.RejectOrder(ctx, e.owner, o.ID, "закончились ингредиенты")
	if err != nil || got.Status != domain.StatusCancelled {
		t.Fatalf("reject failed: %v", err)
	}
}

func TestAccessControl(t *testing.T) {
	ctx := context.Background()
	e := newEnv()
	o := e.create(t, "")
	stranger := auth.Identity{UserID: uuid.NewString(), Role: auth.RoleCustomer}
	if _, err := e.svc.GetOrder(ctx, stranger, o.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("stranger must not see order, got %v", err)
	}
	if _, err := e.svc.GetOrder(ctx, e.owner, o.ID); err != nil {
		t.Fatalf("restaurant owner must see order: %v", err)
	}
	otherOwner := auth.Identity{UserID: uuid.NewString(), Role: auth.RoleRestaurant}
	if _, err := e.svc.AcceptOrder(ctx, otherOwner, o.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("foreign restaurant must not accept order, got %v", err)
	}
	list, _, err := e.svc.ListOrders(ctx, stranger, domain.ListFilter{})
	if err != nil || len(list) != 0 {
		t.Fatalf("stranger must see no orders: %v %d", err, len(list))
	}
}

func TestPickedUpBeforeReadyIsRetried(t *testing.T) {
	ctx := context.Background()
	e := newEnv()
	o := e.create(t, "")
	_ = e.svc.HandlePaymentSucceeded(ctx, uuid.NewString(), payment(o.ID))
	_, _ = e.svc.AcceptOrder(ctx, e.owner, o.ID)
	err := e.svc.HandlePickedUp(ctx, uuid.NewString(), events.DeliveryPayload{OrderID: o.ID})
	if err == nil {
		t.Fatal("out-of-order event must return retryable error")
	}
}
