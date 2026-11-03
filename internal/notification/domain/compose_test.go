package domain

import (
	"strings"
	"testing"

	"github.com/thaperfectcell/food-delivery/pkg/apperr"
	"github.com/thaperfectcell/food-delivery/pkg/events"
)

const (
	orderID    = "3f2a9c1b-0000-4000-8000-000000000001"
	customerID = "c0000000-0000-4000-8000-000000000001"
	ownerID    = "a0000000-0000-4000-8000-000000000001"
	courierID  = "b0000000-0000-4000-8000-000000000001"
)

func orderPayload() events.OrderPayload {
	return events.OrderPayload{
		OrderID:           orderID,
		CustomerID:        customerID,
		RestaurantID:      "r",
		RestaurantOwnerID: ownerID,
		RestaurantName:    "Пиццерия «Марио»",
		RestaurantAddress: "Тверская, 12",
		DeliveryAddress:   "ул. Пушкина, 10",
		Total:             1_297_00,
		ItemsCount:        3,
	}
}

func envelope(t *testing.T, eventType string, payload any) events.Envelope {
	t.Helper()
	e, err := events.New("test", eventType, "", payload)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// recipients возвращает получателей в порядке уведомлений.
func recipients(list []Draft) []string {
	out := make([]string, len(list))
	for i, d := range list {
		out[i] = d.UserID
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestComposeRecipients(t *testing.T) {
	withCourier := orderPayload()
	withCourier.CourierID = courierID
	withCourier.CourierName = "Иван"

	cancelledPaid := orderPayload()
	cancelledPaid.PreviousStatus = "paid"
	cancelledPaid.Reason = "отклонён рестораном"
	cancelledUnpaid := orderPayload()
	cancelledUnpaid.PreviousStatus = "created"

	payment := events.PaymentPayload{OrderID: orderID, CustomerID: customerID, Amount: 1_297_00}
	delivery := events.DeliveryPayload{
		OrderID: orderID, CustomerID: customerID, CourierID: courierID, CourierName: "Иван",
		RestaurantName: "Марио", RestaurantAddress: "Тверская, 12", DeliveryAddress: "Пушкина, 10",
	}
	deliveryNoCourier := delivery
	deliveryNoCourier.CourierID = ""

	cases := []struct {
		eventType string
		payload   any
		want      []string
	}{
		{events.OrderCreated, orderPayload(), []string{customerID}},
		{events.OrderPaid, orderPayload(), []string{customerID, ownerID}},
		{events.OrderAccepted, orderPayload(), []string{customerID}},
		{events.OrderReady, orderPayload(), []string{customerID}},
		{events.OrderReady, withCourier, []string{customerID, courierID}},
		{events.OrderInDelivery, withCourier, []string{customerID}},
		{events.OrderDelivered, orderPayload(), []string{customerID, ownerID}},
		{events.OrderCancelled, cancelledPaid, []string{customerID, ownerID}},
		{events.OrderCancelled, cancelledUnpaid, []string{customerID}},
		{events.PaymentSucceeded, payment, []string{}},
		{events.PaymentFailed, payment, []string{}},
		{events.PaymentRefunded, payment, []string{customerID}},
		{events.DeliveryCourierAssigned, delivery, []string{courierID, customerID}},
		{events.DeliveryPickedUp, delivery, []string{}},
		{events.DeliveryDelivered, delivery, []string{}},
		{events.DeliveryCancelled, delivery, []string{courierID}},
		{events.DeliveryCancelled, deliveryNoCourier, []string{}},
		{"unknown.event", orderPayload(), []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.eventType, func(t *testing.T) {
			drafts, err := Compose(envelope(t, tc.eventType, tc.payload))
			if err != nil {
				t.Fatal(err)
			}
			if got := recipients(drafts); !equal(got, tc.want) {
				t.Fatalf("recipients = %v, want %v", got, tc.want)
			}
			for _, d := range drafts {
				if d.Title == "" || d.Body == "" {
					t.Fatalf("empty text in %+v", d)
				}
				if d.OrderID != orderID {
					t.Fatalf("order id must be set, got %q", d.OrderID)
				}
			}
		})
	}
}

func TestComposeTexts(t *testing.T) {
	paid, _ := Compose(envelope(t, events.OrderPaid, orderPayload()))
	if paid[1].Title != "Новый заказ №3F2A9C1B" {
		t.Errorf("owner title = %q", paid[1].Title)
	}
	if !strings.Contains(paid[1].Body, "1 297 ₽") || !strings.Contains(paid[1].Body, "3 позиции") {
		t.Errorf("owner body = %q", paid[1].Body)
	}

	p := orderPayload()
	p.PreviousStatus = "accepted"
	p.Reason = "закончились ингредиенты"
	cancelled, _ := Compose(envelope(t, events.OrderCancelled, p))
	if cancelled[0].Body != "Причина: закончились ингредиенты." {
		t.Errorf("cancel body = %q", cancelled[0].Body)
	}

	inDelivery := orderPayload()
	inDelivery.CourierName = "Иван"
	d, _ := Compose(envelope(t, events.OrderInDelivery, inDelivery))
	if !strings.Contains(d[0].Body, "Курьер Иван") {
		t.Errorf("in_delivery body = %q", d[0].Body)
	}

	assigned, _ := Compose(envelope(t, events.DeliveryCourierAssigned, events.DeliveryPayload{
		OrderID: orderID, CustomerID: customerID, CourierID: courierID, CourierName: "Иван",
		RestaurantName: "Марио", RestaurantAddress: "Тверская, 12", DeliveryAddress: "Пушкина, 10",
	}))
	if assigned[0].Body != "Заказ №3F2A9C1B: «Марио», Тверская, 12 → Пушкина, 10." {
		t.Errorf("courier body = %q", assigned[0].Body)
	}
	if assigned[1].Body != "Ваш заказ №3F2A9C1B доставит курьер Иван." {
		t.Errorf("customer body = %q", assigned[1].Body)
	}
}

func TestComposeSkipsEmptyRecipients(t *testing.T) {
	p := orderPayload()
	p.RestaurantOwnerID = ""
	drafts, err := Compose(envelope(t, events.OrderPaid, p))
	if err != nil {
		t.Fatal(err)
	}
	if !equal(recipients(drafts), []string{customerID}) {
		t.Fatalf("empty owner must be skipped: %v", recipients(drafts))
	}
}

func TestComposeRejectsMalformedPayload(t *testing.T) {
	e := events.Envelope{ID: "1", Type: events.OrderCreated, Data: []byte(`{"total":"not a number"}`)}
	if _, err := Compose(e); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("expected invalid error, got %v", err)
	}
}

func TestMoney(t *testing.T) {
	cases := map[int64]string{
		0:            "0 ₽",
		99_00:        "99 ₽",
		1_297_00:     "1 297 ₽",
		129_50:       "129,50 ₽",
		1_000_000_00: "1 000 000 ₽",
		-500_00:      "−500 ₽",
	}
	for in, want := range cases {
		if got := Money(in); got != want {
			t.Errorf("Money(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestItems(t *testing.T) {
	cases := map[int]string{1: "1 позиция", 2: "2 позиции", 4: "4 позиции", 5: "5 позиций",
		11: "11 позиций", 12: "12 позиций", 21: "21 позиция", 22: "22 позиции", 0: "0 позиций"}
	for in, want := range cases {
		if got := Items(in); got != want {
			t.Errorf("Items(%d) = %q, want %q", in, got, want)
		}
	}
}
