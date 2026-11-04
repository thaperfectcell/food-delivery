// Package push — исходящий адаптер Notification Service для доставки
// уведомлений в реальном времени через core NATS (без хранения в JetStream:
// push эфемерен, история уведомлений хранится в БД сервиса).
package push

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go"

	"github.com/thaperfectcell/food-delivery/internal/notification/domain"
	"github.com/thaperfectcell/food-delivery/internal/notification/service"
	"github.com/thaperfectcell/food-delivery/pkg/events"
)

// NATS публикует уведомления в субъект notify.user.<id> (реализует service.Pusher).
// API Gateway подписывается на субъект пользователя на время SSE-соединения.
type NATS struct {
	nc *nats.Conn
}

var _ service.Pusher = (*NATS)(nil)

// New создаёт адаптер.
func New(nc *nats.Conn) *NATS { return &NATS{nc: nc} }

// Push публикует уведомление.
func (p *NATS) Push(_ context.Context, n domain.Notification) error {
	data, err := json.Marshal(Message(n))
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}
	if err := p.nc.Publish(events.UserNotifySubject(n.UserID), data); err != nil {
		return fmt.Errorf("publish notification: %w", err)
	}
	return nil
}

// Message преобразует уведомление в сообщение push-канала.
func Message(n domain.Notification) events.UserNotification {
	return events.UserNotification{
		ID:        n.ID,
		UserID:    n.UserID,
		Type:      n.Type,
		Title:     n.Title,
		Body:      n.Body,
		OrderID:   n.OrderID,
		CreatedAt: n.CreatedAt,
	}
}
