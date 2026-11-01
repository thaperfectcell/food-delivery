package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/thaperfectcell/food-delivery/pkg/apperr"
)

var now = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func params() NewOrderParams {
	return NewOrderParams{
		ID:         "o1",
		CustomerID: "c1",
		Restaurant: Restaurant{ID: "r1", OwnerID: "owner", Name: "Марио", Address: "ул. Ленина, 1", Active: true},
		Lines:      []Line{{MenuItemID: "pizza", Quantity: 1}, {MenuItemID: "cola", Quantity: 2}},
		Menu: map[string]MenuItem{
			"pizza": {ID: "pizza", Name: "Маргарита", Price: 549_00, Available: true},
			"cola":  {ID: "cola", Name: "Лимонад", Price: 199_00, Available: true},
			"off":   {ID: "off", Name: "Сезонное", Price: 100_00, Available: false},
		},
		DeliveryAddress: "  ул. Пушкина, 10  ",
		Now:             now,
	}
}

func TestNewOrderCalculatesTotalsFromCatalog(t *testing.T) {
	o, err := NewOrder(params())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if o.ItemsTotal != 549_00+2*199_00 {
		t.Errorf("items total = %d", o.ItemsTotal)
	}
	if o.DeliveryFee != DeliveryFee {
		t.Errorf("delivery fee = %d, want %d", o.DeliveryFee, DeliveryFee)
	}
	if o.Total != o.ItemsTotal+o.DeliveryFee {
		t.Errorf("total = %d", o.Total)
	}
	if o.Status != StatusCreated || len(o.History) != 1 || len(o.PendingChanges()) != 1 {
		t.Errorf("new order must be in status created with one history record")
	}
	if o.DeliveryAddress != "ул. Пушкина, 10" {
		t.Errorf("address must be trimmed, got %q", o.DeliveryAddress)
	}
	if o.ItemsCount() != 3 {
		t.Errorf("items count = %d", o.ItemsCount())
	}
}

func TestNewOrderMergesDuplicateLines(t *testing.T) {
	p := params()
	p.Lines = []Line{{MenuItemID: "pizza", Quantity: 1}, {MenuItemID: "pizza", Quantity: 2}}
	o, err := NewOrder(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Items) != 1 || o.Items[0].Quantity != 3 {
		t.Fatalf("lines must be merged: %+v", o.Items)
	}
}

func TestFreeDeliveryThreshold(t *testing.T) {
	p := params()
	p.Lines = []Line{{MenuItemID: "pizza", Quantity: 3}} // 1647 ₽
	o, err := NewOrder(p)
	if err != nil {
		t.Fatal(err)
	}
	if o.DeliveryFee != 0 || o.Total != o.ItemsTotal {
		t.Fatalf("delivery must be free from %d, got fee %d", FreeDeliveryThreshold, o.DeliveryFee)
	}
	if CalcDeliveryFee(FreeDeliveryThreshold-1) != DeliveryFee {
		t.Fatal("delivery must be paid below threshold")
	}
}

func TestNewOrderValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*NewOrderParams)
		kind   apperr.Kind
	}{
		{"empty", func(p *NewOrderParams) { p.Lines = nil }, apperr.KindInvalid},
		{"zero quantity", func(p *NewOrderParams) { p.Lines[0].Quantity = 0 }, apperr.KindInvalid},
		{"too big quantity", func(p *NewOrderParams) { p.Lines[0].Quantity = 51 }, apperr.KindInvalid},
		{"no address", func(p *NewOrderParams) { p.DeliveryAddress = "   " }, apperr.KindInvalid},
		{"unknown item", func(p *NewOrderParams) { p.Lines = []Line{{MenuItemID: "x", Quantity: 1}} }, apperr.KindInvalid},
		{"unavailable item", func(p *NewOrderParams) {
			p.Lines = append(p.Lines, Line{MenuItemID: "off", Quantity: 1})
		}, apperr.KindPrecondition},
		{"restaurant closed", func(p *NewOrderParams) { p.Restaurant.Active = false }, apperr.KindPrecondition},
		{"below minimum", func(p *NewOrderParams) { p.Lines = []Line{{MenuItemID: "cola", Quantity: 1}} }, apperr.KindPrecondition},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := params()
			tc.mutate(&p)
			_, err := NewOrder(p)
			if err == nil {
				t.Fatal("expected error")
			}
			if apperr.KindOf(err) != tc.kind {
				t.Fatalf("kind = %v, want %v (%v)", apperr.KindOf(err), tc.kind, err)
			}
		})
	}
}

func TestStateMachine(t *testing.T) {
	allowed := map[Status][]Status{
		StatusCreated:    {StatusPaid, StatusCancelled},
		StatusPaid:       {StatusAccepted, StatusCancelled},
		StatusAccepted:   {StatusReady, StatusCancelled},
		StatusReady:      {StatusInDelivery},
		StatusInDelivery: {StatusDelivered},
		StatusDelivered:  {},
		StatusCancelled:  {},
	}
	for from, targets := range allowed {
		for _, to := range AllStatuses {
			want := false
			for _, x := range targets {
				if x == to {
					want = true
				}
			}
			if got := from.CanTransitionTo(to); got != want {
				t.Errorf("%s -> %s: got %v, want %v", from, to, got, want)
			}
		}
	}
	if !StatusDelivered.IsTerminal() || !StatusCancelled.IsTerminal() || StatusReady.IsTerminal() {
		t.Error("terminal statuses are delivered and cancelled")
	}
}

func TestFullLifecycle(t *testing.T) {
	o, _ := NewOrder(params())
	o.ClearPending()
	steps := []Status{StatusPaid, StatusAccepted, StatusReady, StatusInDelivery, StatusDelivered}
	for i, s := range steps {
		if err := o.TransitionTo(s, "", now.Add(time.Duration(i+1)*time.Minute)); err != nil {
			t.Fatalf("transition to %s: %v", s, err)
		}
	}
	if len(o.History) != 6 || len(o.PendingChanges()) != 5 {
		t.Fatalf("history=%d pending=%d", len(o.History), len(o.PendingChanges()))
	}
	err := o.TransitionTo(StatusCancelled, "late", now)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("delivered order must not be cancelled, err=%v", err)
	}
}

func TestCancelStoresReason(t *testing.T) {
	o, _ := NewOrder(params())
	if err := o.TransitionTo(StatusCancelled, "передумал", now); err != nil {
		t.Fatal(err)
	}
	if o.CancelReason != "передумал" || o.Status != StatusCancelled {
		t.Fatalf("unexpected order state: %+v", o)
	}
}
