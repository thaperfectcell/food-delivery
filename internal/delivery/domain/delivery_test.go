package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/thaperfectcell/food-delivery/pkg/auth"
)

var now = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func newDelivery() *Delivery {
	return NewDelivery(NewDeliveryParams{
		ID: "d1", OrderID: "o1", CustomerID: "c1",
		RestaurantName: "Марио", RestaurantAddress: "Тверская, 12", DeliveryAddress: "Пушкина, 10",
		OrderTotal: 1297_00, Now: now,
	})
}

func TestCourierShiftRules(t *testing.T) {
	c := NewCourier("k1", "  ", now)
	if c.Name != "Курьер" || c.Status != CourierOffline {
		t.Fatalf("unexpected new courier %+v", c)
	}
	c.GoOnline(now.Add(time.Minute))
	if c.Status != CourierAvailable || !c.StatusChangedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("courier must be available: %+v", c)
	}
	d := newDelivery()
	if err := d.Assign(c, now); err != nil {
		t.Fatal(err)
	}
	c.GoOnline(now) // занятый курьер остаётся занятым
	if c.Status != CourierBusy {
		t.Fatalf("busy courier must stay busy, got %s", c.Status)
	}
	if err := c.GoOffline(now); !errors.Is(err, ErrCourierBusy) {
		t.Fatalf("busy courier must not go offline, got %v", err)
	}
	c.Release(now)
	if err := c.GoOffline(now); err != nil || c.Status != CourierOffline {
		t.Fatalf("free courier must go offline: %v %s", err, c.Status)
	}
}

func TestDeliveryLifecycle(t *testing.T) {
	c := NewCourier("k1", "Иван", now)
	c.GoOnline(now)
	d := newDelivery()

	if err := d.Assign(c, now); err != nil {
		t.Fatal(err)
	}
	if d.Status != StatusAssigned || d.CourierID != "k1" || d.CourierName != "Иван" || c.Status != CourierBusy {
		t.Fatalf("unexpected state after assign: %+v %+v", d, c)
	}
	if err := d.PickUp("k1", now); !errors.Is(err, ErrOrderNotReady) {
		t.Fatalf("pickup before ready must fail, got %v", err)
	}
	d.MarkReady()
	if err := d.PickUp("k2", now); !errors.Is(err, ErrNotYourDelivery) {
		t.Fatalf("foreign courier must not pick up, got %v", err)
	}
	if err := d.Complete("k1", c, now); !errors.Is(err, ErrNotPickedUp) {
		t.Fatalf("complete before pickup must fail, got %v", err)
	}
	if err := d.PickUp("k1", now); err != nil {
		t.Fatal(err)
	}
	if err := d.Cancel(c, now); !errors.Is(err, ErrCannotCancel) {
		t.Fatalf("picked up delivery must not be cancelled, got %v", err)
	}
	if err := d.Complete("k1", c, now); err != nil {
		t.Fatal(err)
	}
	if d.Status != StatusDelivered || c.Status != CourierAvailable || c.CompletedDeliveries != 1 {
		t.Fatalf("unexpected final state: %+v %+v", d, c)
	}
}

func TestAssignRequiresPendingAndAvailable(t *testing.T) {
	c := NewCourier("k1", "Иван", now) // offline
	d := newDelivery()
	if err := d.Assign(c, now); !errors.Is(err, ErrCannotAssign) {
		t.Fatalf("offline courier must not be assigned, got %v", err)
	}
	c.GoOnline(now)
	cancelled := NewCancelledDelivery(NewDeliveryParams{ID: "d2", OrderID: "o2", Now: now})
	if err := cancelled.Assign(c, now); !errors.Is(err, ErrCannotAssign) {
		t.Fatalf("cancelled delivery must not be assigned, got %v", err)
	}
}

func TestCancelReleasesCourier(t *testing.T) {
	c := NewCourier("k1", "Иван", now)
	c.GoOnline(now)
	d := newDelivery()
	_ = d.Assign(c, now)
	if err := d.Cancel(c, now); err != nil {
		t.Fatal(err)
	}
	if d.Status != StatusCancelled || d.CancelledAt.IsZero() || c.Status != CourierAvailable {
		t.Fatalf("unexpected state: %+v %+v", d, c)
	}
	pending := newDelivery()
	if err := pending.Cancel(nil, now); err != nil || pending.Status != StatusCancelled {
		t.Fatalf("pending delivery must be cancellable: %v", err)
	}
}

func TestCanView(t *testing.T) {
	d := newDelivery()
	d.CourierID = "k1"
	cases := []struct {
		actor auth.Identity
		want  bool
	}{
		{auth.Identity{UserID: "c1", Role: auth.RoleCustomer}, true},
		{auth.Identity{UserID: "c2", Role: auth.RoleCustomer}, false},
		{auth.Identity{UserID: "k1", Role: auth.RoleCourier}, true},
		{auth.Identity{UserID: "k2", Role: auth.RoleCourier}, false},
		{auth.Identity{UserID: "r", Role: auth.RoleRestaurant}, false},
		{auth.Identity{UserID: "a", Role: auth.RoleAdmin}, true},
		{auth.System, true},
	}
	for _, tc := range cases {
		if got := d.CanView(tc.actor); got != tc.want {
			t.Errorf("CanView(%+v) = %v, want %v", tc.actor, got, tc.want)
		}
	}
	if !StatusAssigned.IsActive() || !StatusPickedUp.IsActive() || StatusPending.IsActive() {
		t.Error("active statuses are assigned and picked_up")
	}
}
