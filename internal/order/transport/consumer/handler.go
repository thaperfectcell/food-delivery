// Package consumer — асинхронный входной слой Order Service: подписка на
// события Payment Service и Delivery Service и вызов сценариев бизнес-логики.
package consumer

import (
	"context"
	"fmt"

	"github.com/thaperfectcell/food-delivery/internal/order/service"
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

// Subscribe регистрирует durable-потребителей и возвращает функцию остановки.
func (h *Handler) Subscribe(ctx context.Context, bus *natsbus.Bus) (func(), error) {
	specs := []struct {
		spec    natsbus.ConsumerSpec
		handler natsbus.Handler
	}{
		{natsbus.ConsumerSpec{
			Stream:   events.StreamPayments,
			Durable:  "order-payments",
			Subjects: []string{events.PaymentSucceeded, events.PaymentFailed},
		}, h.onPayment},
		{natsbus.ConsumerSpec{
			Stream:   events.StreamDeliveries,
			Durable:  "order-deliveries",
			Subjects: []string{events.DeliveryCourierAssigned, events.DeliveryPickedUp, events.DeliveryDelivered},
		}, h.onDelivery},
	}
	var stops []func()
	stopAll := func() {
		for _, s := range stops {
			s()
		}
	}
	for _, s := range specs {
		stop, err := bus.Consume(ctx, s.spec, s.handler)
		if err != nil {
			stopAll()
			return nil, err
		}
		stops = append(stops, stop)
	}
	return stopAll, nil
}

func (h *Handler) onPayment(ctx context.Context, e events.Envelope) error {
	var p events.PaymentPayload
	if err := e.Decode(&p); err != nil {
		return fmt.Errorf("%w: %v", natsbus.ErrPermanent, err)
	}
	var err error
	switch e.Type {
	case events.PaymentSucceeded:
		err = h.svc.HandlePaymentSucceeded(ctx, e.ID, p)
	case events.PaymentFailed:
		err = h.svc.HandlePaymentFailed(ctx, e.ID, p)
	default:
		return nil
	}
	return classify(err)
}

func (h *Handler) onDelivery(ctx context.Context, e events.Envelope) error {
	var p events.DeliveryPayload
	if err := e.Decode(&p); err != nil {
		return fmt.Errorf("%w: %v", natsbus.ErrPermanent, err)
	}
	var err error
	switch e.Type {
	case events.DeliveryCourierAssigned:
		err = h.svc.HandleCourierAssigned(ctx, e.ID, p)
	case events.DeliveryPickedUp:
		err = h.svc.HandlePickedUp(ctx, e.ID, p)
	case events.DeliveryDelivered:
		err = h.svc.HandleDelivered(ctx, e.ID, p)
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
