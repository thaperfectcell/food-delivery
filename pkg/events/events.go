// Package events описывает контракт асинхронного взаимодействия сервисов:
// субъекты (типы) доменных событий, потоки JetStream, конверт события и
// полезные нагрузки. Это «опубликованный язык» (Published Language) системы.
package events

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Потоки JetStream и субъекты, которые они хранят.
const (
	StreamOrders     = "ORDERS"
	StreamPayments   = "PAYMENTS"
	StreamDeliveries = "DELIVERIES"
)

// Типы событий (совпадают с субъектами NATS).
const (
	OrderCreated    = "order.created"
	OrderPaid       = "order.paid"
	OrderAccepted   = "order.accepted"
	OrderReady      = "order.ready"
	OrderInDelivery = "order.in_delivery"
	OrderDelivered  = "order.delivered"
	OrderCancelled  = "order.cancelled"

	PaymentSucceeded = "payment.succeeded"
	PaymentFailed    = "payment.failed"
	PaymentRefunded  = "payment.refunded"

	DeliveryCourierAssigned = "delivery.courier_assigned"
	DeliveryPickedUp        = "delivery.picked_up"
	DeliveryDelivered       = "delivery.delivered"
	DeliveryCancelled       = "delivery.cancelled"
)

// StreamSpec — описание потока JetStream.
type StreamSpec struct {
	Name     string
	Subjects []string
}

// Streams — все потоки системы. Каждый сервис при старте идемпотентно
// создаёт/обновляет их (CreateOrUpdateStream), поэтому порядок запуска не важен.
var Streams = []StreamSpec{
	{Name: StreamOrders, Subjects: []string{"order.>"}},
	{Name: StreamPayments, Subjects: []string{"payment.>"}},
	{Name: StreamDeliveries, Subjects: []string{"delivery.>"}},
}

// UserNotifySubject — субъект core NATS для push-уведомлений пользователю
// (Notification Service публикует, API Gateway доставляет клиенту по SSE).
func UserNotifySubject(userID string) string {
	return "notify.user." + userID
}

// Envelope — конверт доменного события.
type Envelope struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Source     string          `json:"source"`
	OccurredAt time.Time       `json:"occurred_at"`
	TraceID    string          `json:"trace_id,omitempty"`
	Data       json.RawMessage `json:"data"`
}

// New создаёт событие с новым идентификатором.
func New(source, eventType, traceID string, payload any) (Envelope, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal %s payload: %w", eventType, err)
	}
	return Envelope{
		ID:         uuid.NewString(),
		Type:       eventType,
		Source:     source,
		OccurredAt: time.Now().UTC(),
		TraceID:    traceID,
		Data:       data,
	}, nil
}

// Decode разбирает полезную нагрузку события.
func (e Envelope) Decode(v any) error {
	if err := json.Unmarshal(e.Data, v); err != nil {
		return fmt.Errorf("decode %s payload: %w", e.Type, err)
	}
	return nil
}

// Marshal сериализует конверт.
func (e Envelope) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

// Parse разбирает конверт из сообщения.
func Parse(data []byte) (Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(data, &e); err != nil {
		return Envelope{}, fmt.Errorf("parse event envelope: %w", err)
	}
	if e.ID == "" || e.Type == "" {
		return Envelope{}, fmt.Errorf("parse event envelope: id and type are required")
	}
	return e, nil
}

// OrderPayload — снимок заказа в событиях order.*.
type OrderPayload struct {
	OrderID           string `json:"order_id"`
	CustomerID        string `json:"customer_id"`
	RestaurantID      string `json:"restaurant_id"`
	RestaurantOwnerID string `json:"restaurant_owner_id"`
	RestaurantName    string `json:"restaurant_name"`
	RestaurantAddress string `json:"restaurant_address"`
	DeliveryAddress   string `json:"delivery_address"`
	Status            string `json:"status"`
	PreviousStatus    string `json:"previous_status,omitempty"`
	Total             int64  `json:"total"`
	ItemsCount        int    `json:"items_count"`
	CourierID         string `json:"courier_id,omitempty"`
	CourierName       string `json:"courier_name,omitempty"`
	Reason            string `json:"reason,omitempty"`
}

// PaymentPayload — данные событий payment.*.
type PaymentPayload struct {
	PaymentID  string `json:"payment_id"`
	OrderID    string `json:"order_id"`
	CustomerID string `json:"customer_id"`
	Amount     int64  `json:"amount"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
}

// DeliveryPayload — данные событий delivery.*.
type DeliveryPayload struct {
	DeliveryID        string `json:"delivery_id"`
	OrderID           string `json:"order_id"`
	CustomerID        string `json:"customer_id"`
	CourierID         string `json:"courier_id,omitempty"`
	CourierName       string `json:"courier_name,omitempty"`
	Status            string `json:"status"`
	RestaurantName    string `json:"restaurant_name"`
	RestaurantAddress string `json:"restaurant_address"`
	DeliveryAddress   string `json:"delivery_address"`
	Reason            string `json:"reason,omitempty"`
}

// UserNotification — сообщение push-уведомления (core NATS → SSE).
type UserNotification struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Type      string    `json:"type"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	OrderID   string    `json:"order_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ShortID — короткий человекочитаемый номер (первые 8 символов UUID).
func ShortID(id string) string {
	if len(id) >= 8 {
		id = id[:8]
	}
	b := []byte(id)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 'a' + 'A'
		}
	}
	return string(b)
}
