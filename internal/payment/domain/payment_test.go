package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/thaperfectcell/food-delivery/pkg/auth"
)

var now = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func TestRefundOnlySucceeded(t *testing.T) {
	p := NewSucceeded("p1", "o1", "c1", 1000_00, "SIM-1", now)
	if err := p.Refund(now.Add(time.Minute)); err != nil {
		t.Fatalf("succeeded payment must be refundable: %v", err)
	}
	if p.Status != StatusRefunded || !p.UpdatedAt.After(p.CreatedAt) {
		t.Fatalf("unexpected state: %+v", p)
	}
	if err := p.Refund(now); !errors.Is(err, ErrNotRefundable) {
		t.Fatalf("refunded payment must not be refunded twice, got %v", err)
	}

	f := NewFailed("p2", "o2", "c1", 1000_00, "лимит", now)
	if err := f.Refund(now); !errors.Is(err, ErrNotRefundable) {
		t.Fatalf("failed payment must not be refundable, got %v", err)
	}
	if f.FailureReason != "лимит" || f.Status != StatusFailed {
		t.Fatalf("unexpected failed payment: %+v", f)
	}
}

func TestCanView(t *testing.T) {
	p := NewSucceeded("p1", "o1", "c1", 100, "SIM-1", now)
	cases := []struct {
		actor auth.Identity
		want  bool
	}{
		{auth.Identity{UserID: "c1", Role: auth.RoleCustomer}, true},
		{auth.Identity{UserID: "c2", Role: auth.RoleCustomer}, false},
		{auth.Identity{UserID: "c1", Role: auth.RoleCourier}, false},
		{auth.Identity{UserID: "a", Role: auth.RoleAdmin}, true},
		{auth.System, true},
		{auth.Identity{}, false},
	}
	for _, tc := range cases {
		if got := p.CanView(tc.actor); got != tc.want {
			t.Errorf("CanView(%+v) = %v, want %v", tc.actor, got, tc.want)
		}
	}
}

func TestStatusValid(t *testing.T) {
	for _, s := range []Status{StatusPending, StatusSucceeded, StatusFailed, StatusRefunded} {
		if !s.Valid() {
			t.Errorf("%s must be valid", s)
		}
	}
	if Status("unknown").Valid() {
		t.Error("unknown status must be invalid")
	}
}
