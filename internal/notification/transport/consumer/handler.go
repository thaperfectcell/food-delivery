// Package consumer — асинхронный входной слой Notification Service: подписка
// на все доменные события заказов, платежей и доставок.
package consumer

import (
	"context"
	"fmt"

	"github.com/thaperfectcell/food-delivery/internal/notification/service"
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

// Specs — durable-потребители сервиса: по одному на поток, все субъекты потока.
var Specs = []natsbus.ConsumerSpec{
	{Stream: events.StreamOrders, Durable: "notification-orders", Subjects: []string{"order.>"}},
	{Stream: events.StreamPayments, Durable: "notification-payments", Subjects: []string{"payment.>"}},
	{Stream: events.StreamDeliveries, Durable: "notification-deliveries", Subjects: []string{"delivery.>"}},
}

// Subscribe регистрирует потребителей и возвращает функцию остановки.
func (h *Handler) Subscribe(ctx context.Context, bus *natsbus.Bus) (func(), error) {
	var stops []func()
	stopAll := func() {
		for _, s := range stops {
			s()
		}
	}
	for _, spec := range Specs {
		stop, err := bus.Consume(ctx, spec, h.onEvent)
		if err != nil {
			stopAll()
			return nil, err
		}
		stops = append(stops, stop)
	}
	return stopAll, nil
}

func (h *Handler) onEvent(ctx context.Context, e events.Envelope) error {
	return classify(h.svc.HandleEvent(ctx, e))
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
