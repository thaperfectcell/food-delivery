package service

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thaperfectcell/food-delivery/internal/delivery/domain"
	"github.com/thaperfectcell/food-delivery/pkg/apperr"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
	"github.com/thaperfectcell/food-delivery/pkg/events"
	"github.com/thaperfectcell/food-delivery/pkg/logger"
)

// ------------------------------------------------------------ тестовые двойники

// memRepo моделирует хранилище в памяти; мьютекс заменяет блокировки строк.
type memRepo struct {
	mu         sync.Mutex
	couriers   map[string]*domain.Courier
	deliveries map[string]*domain.Delivery
	seq        map[string]int // порядок вставки доставок (для FIFO при равном времени)
	nextSeq    int
	processed  map[string]bool
	outbox     []events.Envelope
}

func newMemRepo() *memRepo {
	return &memRepo{
		couriers:   map[string]*domain.Courier{},
		deliveries: map[string]*domain.Delivery{},
		seq:        map[string]int{},
		processed:  map[string]bool{},
	}
}

func cloneC(c *domain.Courier) *domain.Courier {
	if c == nil {
		return nil
	}
	x := *c
	return &x
}

func cloneD(d *domain.Delivery) *domain.Delivery {
	x := *d
	return &x
}

func (r *memRepo) withName(d *domain.Delivery) *domain.Delivery {
	x := cloneD(d)
	if c, ok := r.couriers[x.CourierID]; ok {
		x.CourierName = c.Name
	}
	return x
}

func (r *memRepo) GetCourier(_ context.Context, id string) (*domain.Courier, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.couriers[id]
	if !ok {
		return nil, domain.ErrCourierNotFound
	}
	return cloneC(c), nil
}

func (r *memRepo) UpsertCourier(_ context.Context, id, name string, now time.Time, fn CourierMutateFunc) (*domain.Courier, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.couriers[id]
	if !ok {
		c = domain.NewCourier(id, name, now)
	} else if name != "" {
		c = cloneC(c)
		c.Name = name
	}
	work := cloneC(c)
	if err := fn(work); err != nil {
		return nil, err
	}
	r.couriers[id] = work
	return cloneC(work), nil
}

func (r *memRepo) GetDelivery(_ context.Context, id string) (*domain.Delivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.deliveries[id]
	if !ok {
		return nil, domain.ErrDeliveryNotFound
	}
	return r.withName(d), nil
}

func (r *memRepo) byOrder(orderID string) *domain.Delivery {
	for _, d := range r.deliveries {
		if d.OrderID == orderID {
			return d
		}
	}
	return nil
}

func (r *memRepo) GetDeliveryByOrder(_ context.Context, orderID string) (*domain.Delivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.byOrder(orderID)
	if d == nil {
		return nil, domain.ErrDeliveryNotFound
	}
	return r.withName(d), nil
}

func (r *memRepo) ListCourierDeliveries(_ context.Context, courierID string, activeOnly bool, limit int) ([]*domain.Delivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.Delivery
	for _, d := range r.deliveries {
		if d.CourierID != courierID || (activeOnly && !d.Status.IsActive()) {
			continue
		}
		out = append(out, r.withName(d))
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *memRepo) CreateOnce(_ context.Context, eventID, _ string, d *domain.Delivery) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.processed[eventID] {
		return false, nil
	}
	if r.byOrder(d.OrderID) != nil {
		return false, domain.ErrDeliveryExists
	}
	r.deliveries[d.ID] = cloneD(d)
	r.seq[d.ID] = r.nextSeq
	r.nextSeq++
	r.processed[eventID] = true
	return true, nil
}

func (r *memRepo) mutate(d *domain.Delivery, fn DeliveryMutateFunc) (*domain.Delivery, error) {
	work := r.withName(d)
	var c *domain.Courier
	if work.CourierID != "" {
		c = cloneC(r.couriers[work.CourierID])
	}
	evts, err := fn(work, c)
	if err != nil {
		return nil, err
	}
	r.deliveries[work.ID] = work
	if c != nil {
		r.couriers[c.ID] = c
	}
	r.outbox = append(r.outbox, evts...)
	return cloneD(work), nil
}

func (r *memRepo) UpdateDelivery(_ context.Context, id string, fn DeliveryMutateFunc) (*domain.Delivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.deliveries[id]
	if !ok {
		return nil, domain.ErrDeliveryNotFound
	}
	return r.mutate(d, fn)
}

func (r *memRepo) UpdateByOrderOnce(_ context.Context, eventID, _, orderID string, fn DeliveryMutateFunc) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.processed[eventID] {
		return false, nil
	}
	d := r.byOrder(orderID)
	if d == nil {
		return false, domain.ErrDeliveryNotFound
	}
	if _, err := r.mutate(d, fn); err != nil {
		return false, err
	}
	r.processed[eventID] = true
	return true, nil
}

func (r *memRepo) AssignNext(_ context.Context, fn DeliveryMutateFunc) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var pending []*domain.Delivery
	for _, d := range r.deliveries {
		if d.Status == domain.StatusPending {
			pending = append(pending, d)
		}
	}
	var free []*domain.Courier
	for _, c := range r.couriers {
		if c.Status == domain.CourierAvailable {
			free = append(free, c)
		}
	}
	if len(pending) == 0 || len(free) == 0 {
		return false, nil
	}
	sort.Slice(pending, func(i, j int) bool {
		if !pending[i].CreatedAt.Equal(pending[j].CreatedAt) {
			return pending[i].CreatedAt.Before(pending[j].CreatedAt)
		}
		return r.seq[pending[i].ID] < r.seq[pending[j].ID]
	})
	sort.Slice(free, func(i, j int) bool {
		if !free[i].StatusChangedAt.Equal(free[j].StatusChangedAt) {
			return free[i].StatusChangedAt.Before(free[j].StatusChangedAt)
		}
		return free[i].ID < free[j].ID
	})
	d, c := cloneD(pending[0]), cloneC(free[0])
	evts, err := fn(d, c)
	if err != nil {
		return false, err
	}
	r.deliveries[d.ID] = d
	r.couriers[c.ID] = c
	r.outbox = append(r.outbox, evts...)
	return true, nil
}

func (r *memRepo) Stats(context.Context) (domain.Stats, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var st domain.Stats
	for _, c := range r.couriers {
		if c.Status != domain.CourierOffline {
			st.CouriersOnline++
		}
		if c.Status == domain.CourierBusy {
			st.CouriersBusy++
		}
	}
	for _, d := range r.deliveries {
		switch {
		case d.Status == domain.StatusPending:
			st.DeliveriesPending++
		case d.Status.IsActive():
			st.DeliveriesActive++
		case d.Status == domain.StatusDelivered:
			st.DeliveriesCompleted++
		}
	}
	return st, nil
}

func (r *memRepo) eventTypes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.outbox))
	for i, e := range r.outbox {
		out[i] = e.Type
	}
	return out
}

// ------------------------------------------------------------ окружение теста

type env struct {
	svc  *Service
	repo *memRepo
	ctx  context.Context
}

func newEnv() *env {
	repo := newMemRepo()
	svc := New(repo, logger.Discard())
	clock := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	svc.now = func() time.Time { // часы, идущие вперёд на секунду при каждом обращении
		mu.Lock()
		defer mu.Unlock()
		clock = clock.Add(time.Second)
		return clock
	}
	return &env{svc: svc, repo: repo, ctx: context.Background()}
}

func courier(name string) auth.Identity {
	return auth.Identity{UserID: uuid.NewString(), Role: auth.RoleCourier, Name: name}
}

func orderEvent() events.OrderPayload {
	return events.OrderPayload{
		OrderID: uuid.NewString(), CustomerID: uuid.NewString(),
		RestaurantName: "Марио", RestaurantAddress: "Тверская, 12", DeliveryAddress: "Пушкина, 10", Total: 1297_00,
	}
}

func (e *env) accept(t *testing.T, o events.OrderPayload) {
	t.Helper()
	if err := e.svc.HandleOrderAccepted(e.ctx, uuid.NewString(), o); err != nil {
		t.Fatalf("order.accepted: %v", err)
	}
}

func (e *env) online(t *testing.T, c auth.Identity) *domain.Courier {
	t.Helper()
	got, err := e.svc.SetCourierStatus(e.ctx, c, true, "")
	if err != nil {
		t.Fatalf("go online: %v", err)
	}
	return got
}

func (e *env) delivery(t *testing.T, orderID string) *domain.Delivery {
	t.Helper()
	d, err := e.repo.GetDeliveryByOrder(e.ctx, orderID)
	if err != nil {
		t.Fatalf("delivery for order %s: %v", orderID, err)
	}
	return d
}

// ------------------------------------------------------------ тесты

func TestDeliveryWaitsForCourierAndIsAssignedOnShiftStart(t *testing.T) {
	e := newEnv()
	o := orderEvent()
	e.accept(t, o)
	if d := e.delivery(t, o.OrderID); d.Status != domain.StatusPending {
		t.Fatalf("delivery must wait for a courier, got %s", d.Status)
	}

	ivan := courier("Иван")
	c := e.online(t, ivan)
	if c.Status != domain.CourierBusy {
		t.Fatalf("courier must get the pending order right after going online, status %s", c.Status)
	}
	d := e.delivery(t, o.OrderID)
	if d.Status != domain.StatusAssigned || d.CourierID != ivan.UserID || d.CourierName != "Иван" {
		t.Fatalf("unexpected delivery %+v", d)
	}
	types := e.repo.eventTypes()
	if len(types) != 1 || types[0] != events.DeliveryCourierAssigned {
		t.Fatalf("expected delivery.courier_assigned, got %v", types)
	}
}

func TestFIFOAssignmentAndFullLifecycle(t *testing.T) {
	e := newEnv()
	first, second := orderEvent(), orderEvent()
	ivan := courier("Иван")
	e.online(t, ivan)
	e.accept(t, first)
	e.accept(t, second)

	d1, d2 := e.delivery(t, first.OrderID), e.delivery(t, second.OrderID)
	if d1.Status != domain.StatusAssigned || d2.Status != domain.StatusPending {
		t.Fatalf("first order must be assigned, second must wait: %s %s", d1.Status, d2.Status)
	}

	// Курьер не может забрать заказ, пока ресторан его не приготовил.
	if _, err := e.svc.PickUp(e.ctx, ivan, d1.ID); !errors.Is(err, domain.ErrOrderNotReady) {
		t.Fatalf("pickup before ready must fail, got %v", err)
	}
	if err := e.svc.HandleOrderReady(e.ctx, uuid.NewString(), first); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PickUp(e.ctx, courier("Чужой"), d1.ID); !errors.Is(err, domain.ErrNotYourDelivery) {
		t.Fatalf("foreign courier must be rejected, got %v", err)
	}
	if _, err := e.svc.SetCourierStatus(e.ctx, ivan, false, ""); !errors.Is(err, domain.ErrCourierBusy) {
		t.Fatalf("busy courier must not go offline, got %v", err)
	}
	if _, err := e.svc.PickUp(e.ctx, ivan, d1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Complete(e.ctx, ivan, d1.ID); err != nil {
		t.Fatal(err)
	}

	// Освободившийся курьер сразу получает следующий заказ.
	if d := e.delivery(t, second.OrderID); d.Status != domain.StatusAssigned || d.CourierID != ivan.UserID {
		t.Fatalf("second order must be assigned after completion: %+v", d)
	}
	c, _ := e.svc.GetCourier(e.ctx, ivan, "")
	if c.CompletedDeliveries != 1 || c.Status != domain.CourierBusy {
		t.Fatalf("unexpected courier %+v", c)
	}
	want := []string{
		events.DeliveryCourierAssigned, events.DeliveryPickedUp,
		events.DeliveryDelivered, events.DeliveryCourierAssigned,
	}
	got := e.repo.eventTypes()
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
}

func TestLongestWaitingCourierGetsOrder(t *testing.T) {
	e := newEnv()
	early, late := courier("Ранний"), courier("Поздний")
	e.online(t, early)
	e.online(t, late)
	o := orderEvent()
	e.accept(t, o)
	if d := e.delivery(t, o.OrderID); d.CourierID != early.UserID {
		t.Fatalf("courier waiting longer must get the order, got %s", d.CourierName)
	}
}

func TestCancelAssignedDeliveryFreesCourier(t *testing.T) {
	e := newEnv()
	ivan := courier("Иван")
	e.online(t, ivan)
	cancelled, waiting := orderEvent(), orderEvent()
	e.accept(t, cancelled)
	e.accept(t, waiting)

	cancelled.Reason = "отклонён рестораном"
	if err := e.svc.HandleOrderCancelled(e.ctx, uuid.NewString(), cancelled); err != nil {
		t.Fatal(err)
	}
	if d := e.delivery(t, cancelled.OrderID); d.Status != domain.StatusCancelled {
		t.Fatalf("delivery must be cancelled, got %s", d.Status)
	}
	// Освобождённый курьер получает ожидающий заказ.
	if d := e.delivery(t, waiting.OrderID); d.Status != domain.StatusAssigned || d.CourierID != ivan.UserID {
		t.Fatalf("waiting order must be assigned to the freed courier: %+v", d)
	}
	var cancelledEvt *events.Envelope
	for i, ev := range e.repo.outbox {
		if ev.Type == events.DeliveryCancelled {
			cancelledEvt = &e.repo.outbox[i]
		}
	}
	if cancelledEvt == nil {
		t.Fatal("delivery.cancelled must be published")
	}
	var p events.DeliveryPayload
	_ = cancelledEvt.Decode(&p)
	if p.CourierID != ivan.UserID || p.Reason != "отклонён рестораном" {
		t.Fatalf("cancel event must carry courier and reason: %+v", p)
	}
}

func TestCancelBeforeAcceptIgnoresLateAccept(t *testing.T) {
	e := newEnv()
	e.online(t, courier("Иван"))
	o := orderEvent()
	if err := e.svc.HandleOrderCancelled(e.ctx, uuid.NewString(), o); err != nil {
		t.Fatal(err)
	}
	e.accept(t, o) // запоздавшее событие
	if d := e.delivery(t, o.OrderID); d.Status != domain.StatusCancelled || d.CourierID != "" {
		t.Fatalf("late order.accepted must be ignored: %+v", d)
	}
	if n := len(e.repo.eventTypes()); n != 0 {
		t.Fatalf("no events expected, got %d", n)
	}
}

func TestReadyBeforeAcceptIsRetried(t *testing.T) {
	e := newEnv()
	err := e.svc.HandleOrderReady(e.ctx, uuid.NewString(), orderEvent())
	if !apperr.Is(err, apperr.KindUnavailable) {
		t.Fatalf("expected retryable error, got %v", err)
	}
}

func TestDuplicateAcceptCreatesSingleDelivery(t *testing.T) {
	e := newEnv()
	o := orderEvent()
	id := uuid.NewString()
	for i := 0; i < 3; i++ {
		if err := e.svc.HandleOrderAccepted(e.ctx, id, o); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.svc.HandleOrderAccepted(e.ctx, uuid.NewString(), o); err != nil {
		t.Fatal(err)
	}
	if n := len(e.repo.deliveries); n != 1 {
		t.Fatalf("expected one delivery, got %d", n)
	}
}

func TestInvalidEventIsRejected(t *testing.T) {
	e := newEnv()
	err := e.svc.HandleOrderAccepted(e.ctx, uuid.NewString(), events.OrderPayload{OrderID: "x"})
	if !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("expected invalid error, got %v", err)
	}
}

func TestAccessRules(t *testing.T) {
	e := newEnv()
	ivan := courier("Иван")
	e.online(t, ivan)
	o := orderEvent()
	e.accept(t, o)

	customer := auth.Identity{UserID: o.CustomerID, Role: auth.RoleCustomer}
	if _, err := e.svc.GetDeliveryByOrder(e.ctx, customer, o.OrderID); err != nil {
		t.Fatalf("customer must see own delivery: %v", err)
	}
	if _, err := e.svc.GetDeliveryByOrder(e.ctx, ivan, o.OrderID); err != nil {
		t.Fatalf("assigned courier must see delivery: %v", err)
	}
	if _, err := e.svc.GetDeliveryByOrder(e.ctx, courier("Чужой"), o.OrderID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("foreign courier must be forbidden, got %v", err)
	}
	if _, err := e.svc.GetDeliveryByOrder(e.ctx, auth.System, uuid.NewString()); !errors.Is(err, domain.ErrDeliveryNotFound) {
		t.Fatalf("unknown order must be not found, got %v", err)
	}
	if _, err := e.svc.SetCourierStatus(e.ctx, customer, true, ""); !apperr.Is(err, apperr.KindForbidden) {
		t.Fatalf("customer must not change courier status, got %v", err)
	}
	if _, err := e.svc.Stats(e.ctx, ivan); !apperr.Is(err, apperr.KindForbidden) {
		t.Fatalf("courier must not see stats, got %v", err)
	}
	st, err := e.svc.Stats(e.ctx, auth.Identity{UserID: "a", Role: auth.RoleAdmin})
	if err != nil || st.CouriersOnline != 1 || st.CouriersBusy != 1 || st.DeliveriesActive != 1 {
		t.Fatalf("unexpected stats %+v (%v)", st, err)
	}
	list, err := e.svc.ListCourierDeliveries(e.ctx, ivan, true, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("courier must see one active delivery: %v %d", err, len(list))
	}
}

func TestNewCourierProfileIsOffline(t *testing.T) {
	e := newEnv()
	olga := courier("Ольга")
	c, err := e.svc.GetCourier(e.ctx, olga, "")
	if err != nil || c.Status != domain.CourierOffline || c.Name != "Ольга" {
		t.Fatalf("courier without profile must be offline: %+v %v", c, err)
	}
	if _, err := e.svc.GetCourier(e.ctx, olga, uuid.NewString()); !apperr.Is(err, apperr.KindForbidden) {
		t.Fatalf("courier must not read other profiles, got %v", err)
	}
	got, err := e.svc.SetCourierStatus(e.ctx, olga, true, "  Ольга С.  ")
	if err != nil || got.Name != "Ольга С." || got.Status != domain.CourierAvailable {
		t.Fatalf("unexpected courier %+v (%v)", got, err)
	}
	off, err := e.svc.SetCourierStatus(e.ctx, olga, false, "")
	if err != nil || off.Status != domain.CourierOffline || off.Name != "Ольга С." {
		t.Fatalf("unexpected courier %+v (%v)", off, err)
	}
}
