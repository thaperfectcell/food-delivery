// Package consumer — асинхронный входной слой Payment Service: подписка на
// события заказов (order.created, order.cancelled) и вызов бизнес-логики.
package consumer

import (
	"context"
	"fmt"

	"github.com/thaperfectcell/food-delivery/internal/payment/service"
	"github.com/thaperfectcell/food-delivery/pkg/apperr"
	"github.com/thaperfectcell/food-delivery/pkg/events"
	"github.com/thaperfectcell/food-delivery/pkg/natsbus"
)

// Handler связывает события с сервисом.
type Handler struct {
	svc *service.Service
}

// New создаёт обработчик событий.
func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

// Subscribe регистрирует durable-потребителя и возвращает функцию остановки.
func (h *Handler) Subscribe(ctx context.Context, bus *natsbus.Bus) (func(), error) {
	return bus.Consume(ctx, natsbus.ConsumerSpec{
		Stream:   events.StreamOrders,
		Durable:  "payment",
		Subjects: []string{events.OrderCreated, events.OrderCancelled},
	}, h.onOrder)
}

func (h *Handler) onOrder(ctx context.Context, e events.Envelope) error {
	var p events.OrderPayload
	if err := e.Decode(&p); err != nil {
		return fmt.Errorf("%w: %v", natsbus.ErrPermanent, err)
	}
	var err error
	switch e.Type {
	case events.OrderCreated:
		err = h.svc.HandleOrderCreated(ctx, e.ID, p)
	case events.OrderCancelled:
		err = h.svc.HandleOrderCancelled(ctx, e.ID, p)
	default:
		return nil
	}
	return classify(err)
}

// classify отделяет неустранимые ошибки (повтор не поможет) от временных.
func classify(err error) error {
	if err == nil {
		return nil
	}
	switch apperr.KindOf(err) {
	case apperr.KindInvalid, apperr.KindNotFound:
		return fmt.Errorf("%w: %v", natsbus.ErrPermanent, err)
	}
	return err
}
