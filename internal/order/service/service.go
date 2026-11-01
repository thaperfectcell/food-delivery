// Package service — слой бизнес-логики Order Service (сценарии использования).
//
// Слой зависит только от доменной модели и от интерфейсов Repository и Catalog
// (порты), реализации которых предоставляются слоем доступа к данным и
// клиентскими адаптерами. Благодаря этому логика тестируется без БД и сети.
package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thaperfectcell/food-delivery/internal/order/domain"
	"github.com/thaperfectcell/food-delivery/pkg/apperr"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
	"github.com/thaperfectcell/food-delivery/pkg/events"
	"github.com/thaperfectcell/food-delivery/pkg/requestid"
)

// MutateFunc изменяет заблокированный заказ и возвращает события для outbox.
type MutateFunc func(o *domain.Order) ([]events.Envelope, error)

// Repository — порт хранилища заказов.
type Repository interface {
	// Create атомарно сохраняет заказ и события.
	Create(ctx context.Context, o *domain.Order, evts []events.Envelope) error
	Get(ctx context.Context, id string) (*domain.Order, error)
	GetByIdempotencyKey(ctx context.Context, customerID, key string) (*domain.Order, error)
	List(ctx context.Context, f domain.ListFilter) ([]*domain.Order, int, error)
	// Update блокирует заказ (SELECT … FOR UPDATE), применяет fn и атомарно
	// сохраняет изменения и события.
	Update(ctx context.Context, id string, fn MutateFunc) (*domain.Order, error)
	// UpdateOnce — то же, но не более одного раза для события eventID
	// (идемпотентный потребитель). applied=false, если событие уже обработано.
	UpdateOnce(ctx context.Context, eventID, eventType, orderID string, fn MutateFunc) (applied bool, err error)
	Stats(ctx context.Context, since time.Time) (domain.Stats, error)
}

// Catalog — порт каталога ресторанов (Restaurant Service, синхронный gRPC).
type Catalog interface {
	GetMenuItems(ctx context.Context, restaurantID string, ids []string) (domain.Restaurant, []domain.MenuItem, error)
}

// Service реализует сценарии использования заказов.
type Service struct {
	repo    Repository
	catalog Catalog
	log     *slog.Logger
	now     func() time.Time
	newID   func() string
}

// New создаёт сервис.
func New(repo Repository, catalog Catalog, log *slog.Logger) *Service {
	return &Service{
		repo:    repo,
		catalog: catalog,
		log:     log,
		now:     func() time.Time { return time.Now().UTC() },
		newID:   uuid.NewString,
	}
}

// CreateOrderInput — данные для оформления заказа.
type CreateOrderInput struct {
	RestaurantID    string
	Lines           []domain.Line
	DeliveryAddress string
	Comment         string
	IdempotencyKey  string
}

// CreateOrder оформляет заказ клиента:
//  1. идемпотентность по ключу клиента;
//  2. синхронная проверка позиций и цен в каталоге (gRPC к Restaurant Service);
//  3. расчёт стоимости по доменным правилам;
//  4. атомарное сохранение заказа и события order.created (outbox).
func (s *Service) CreateOrder(ctx context.Context, actor auth.Identity, in CreateOrderInput) (*domain.Order, error) {
	if !actor.Is(auth.RoleCustomer) {
		return nil, apperr.Forbidden("оформлять заказы могут только клиенты")
	}
	if _, err := uuid.Parse(in.RestaurantID); err != nil {
		return nil, apperr.Invalid("некорректный идентификатор ресторана")
	}
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	if len(in.IdempotencyKey) > 100 {
		return nil, apperr.Invalid("ключ идемпотентности слишком длинный")
	}
	if in.IdempotencyKey != "" {
		existing, err := s.repo.GetByIdempotencyKey(ctx, actor.UserID, in.IdempotencyKey)
		if err == nil {
			return existing, nil // повтор того же запроса — возвращаем уже созданный заказ
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
	}

	ids := make([]string, 0, len(in.Lines))
	for _, l := range in.Lines {
		if _, err := uuid.Parse(l.MenuItemID); err != nil {
			return nil, apperr.Invalid("некорректный идентификатор позиции меню")
		}
		ids = append(ids, l.MenuItemID)
	}
	if len(ids) == 0 {
		return nil, domain.ErrEmptyOrder
	}

	restaurant, items, err := s.catalog.GetMenuItems(ctx, in.RestaurantID, ids)
	if err != nil {
		return nil, err
	}
	menu := make(map[string]domain.MenuItem, len(items))
	for _, it := range items {
		menu[it.ID] = it
	}

	o, err := domain.NewOrder(domain.NewOrderParams{
		ID:              s.newID(),
		CustomerID:      actor.UserID,
		Restaurant:      restaurant,
		Lines:           in.Lines,
		Menu:            menu,
		DeliveryAddress: in.DeliveryAddress,
		Comment:         in.Comment,
		IdempotencyKey:  in.IdempotencyKey,
		Now:             s.now(),
	})
	if err != nil {
		return nil, err
	}
	evt, err := s.event(ctx, events.OrderCreated, o, "", "")
	if err != nil {
		return nil, err
	}
	if err := s.repo.Create(ctx, o, []events.Envelope{evt}); err != nil {
		if apperr.Is(err, apperr.KindConflict) && o.IdempotencyKey != "" {
			// Гонка двух одинаковых запросов: возвращаем заказ-победитель.
			return s.repo.GetByIdempotencyKey(ctx, actor.UserID, o.IdempotencyKey)
		}
		return nil, err
	}
	s.log.InfoContext(ctx, "order created",
		slog.String("order_id", o.ID), slog.String("restaurant_id", o.RestaurantID),
		slog.Int64("total", o.Total))
	return o, nil
}

// GetOrder возвращает заказ, если у вызывающего есть к нему доступ.
func (s *Service) GetOrder(ctx context.Context, actor auth.Identity, id string) (*domain.Order, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, domain.ErrNotFound
	}
	o, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !canView(actor, o) {
		return nil, domain.ErrForbidden
	}
	return o, nil
}

// ListOrders возвращает заказы с учётом роли: клиент — свои, ресторан — заказы
// своих ресторанов, администратор — все.
func (s *Service) ListOrders(ctx context.Context, actor auth.Identity, f domain.ListFilter) ([]*domain.Order, int, error) {
	switch {
	case actor.Privileged():
	case actor.Is(auth.RoleCustomer):
		f.CustomerID = actor.UserID
	case actor.Is(auth.RoleRestaurant):
		f.RestaurantOwnerID = actor.UserID
	default:
		return nil, 0, domain.ErrForbidden
	}
	if f.RestaurantID != "" {
		if _, err := uuid.Parse(f.RestaurantID); err != nil {
			return nil, 0, apperr.Invalid("некорректный идентификатор ресторана")
		}
	}
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	return s.repo.List(ctx, f)
}

// CancelOrder — отмена заказа клиентом до того, как ресторан его принял.
// Если заказ уже оплачен, Payment Service выполнит возврат по событию order.cancelled.
func (s *Service) CancelOrder(ctx context.Context, actor auth.Identity, id, reason string) (*domain.Order, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "отменён клиентом"
	}
	return s.mutate(ctx, id, func(o *domain.Order) ([]events.Envelope, error) {
		if !(actor.Privileged() || (actor.Is(auth.RoleCustomer) && o.CustomerID == actor.UserID)) {
			return nil, domain.ErrForbidden
		}
		if o.Status != domain.StatusCreated && o.Status != domain.StatusPaid {
			return nil, apperr.Precondition("заказ уже принят рестораном — отменить его нельзя")
		}
		return s.transition(ctx, o, domain.StatusCancelled, events.OrderCancelled, reason)
	})
}

// AcceptOrder — ресторан принимает оплаченный заказ в работу.
func (s *Service) AcceptOrder(ctx context.Context, actor auth.Identity, id string) (*domain.Order, error) {
	return s.mutate(ctx, id, func(o *domain.Order) ([]events.Envelope, error) {
		if !isRestaurantOwner(actor, o) {
			return nil, domain.ErrForbidden
		}
		return s.transition(ctx, o, domain.StatusAccepted, events.OrderAccepted, "")
	})
}

// RejectOrder — ресторан отклоняет заказ (до передачи курьеру).
func (s *Service) RejectOrder(ctx context.Context, actor auth.Identity, id, reason string) (*domain.Order, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "отклонён рестораном"
	}
	return s.mutate(ctx, id, func(o *domain.Order) ([]events.Envelope, error) {
		if !isRestaurantOwner(actor, o) {
			return nil, domain.ErrForbidden
		}
		if o.Status == domain.StatusCreated {
			return nil, apperr.Precondition("заказ ещё не оплачен")
		}
		return s.transition(ctx, o, domain.StatusCancelled, events.OrderCancelled, reason)
	})
}

// MarkReady — ресторан сообщает, что заказ готов к выдаче курьеру.
func (s *Service) MarkReady(ctx context.Context, actor auth.Identity, id string) (*domain.Order, error) {
	return s.mutate(ctx, id, func(o *domain.Order) ([]events.Envelope, error) {
		if !isRestaurantOwner(actor, o) {
			return nil, domain.ErrForbidden
		}
		return s.transition(ctx, o, domain.StatusReady, events.OrderReady, "")
	})
}

// Stats — статистика для администратора.
func (s *Service) Stats(ctx context.Context, actor auth.Identity) (domain.Stats, error) {
	if !actor.Privileged() {
		return domain.Stats{}, domain.ErrForbidden
	}
	now := s.now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return s.repo.Stats(ctx, dayStart)
}

// ---------------------------------------------------------- реакции на события

// HandlePaymentSucceeded: created → paid.
func (s *Service) HandlePaymentSucceeded(ctx context.Context, eventID string, p events.PaymentPayload) error {
	return s.applyEvent(ctx, eventID, events.PaymentSucceeded, p.OrderID, func(o *domain.Order) ([]events.Envelope, error) {
		if o.Status != domain.StatusCreated {
			// Заказ мог быть отменён клиентом во время оплаты — тогда Payment Service
			// вернёт средства, получив order.cancelled. Повторы игнорируются.
			s.log.WarnContext(ctx, "payment succeeded for order in unexpected status",
				slog.String("order_id", o.ID), slog.String("status", string(o.Status)))
			return nil, nil
		}
		return s.transition(ctx, o, domain.StatusPaid, events.OrderPaid, "")
	})
}

// HandlePaymentFailed: created → cancelled.
func (s *Service) HandlePaymentFailed(ctx context.Context, eventID string, p events.PaymentPayload) error {
	return s.applyEvent(ctx, eventID, events.PaymentFailed, p.OrderID, func(o *domain.Order) ([]events.Envelope, error) {
		if o.Status != domain.StatusCreated {
			return nil, nil
		}
		reason := "оплата не прошла"
		if p.Reason != "" {
			reason += ": " + p.Reason
		}
		return s.transition(ctx, o, domain.StatusCancelled, events.OrderCancelled, reason)
	})
}

// HandleCourierAssigned сохраняет назначенного курьера (статус не меняется).
func (s *Service) HandleCourierAssigned(ctx context.Context, eventID string, p events.DeliveryPayload) error {
	return s.applyEvent(ctx, eventID, events.DeliveryCourierAssigned, p.OrderID, func(o *domain.Order) ([]events.Envelope, error) {
		if o.Status.IsTerminal() {
			return nil, nil
		}
		o.AssignCourier(p.CourierID, p.CourierName, s.now())
		return nil, nil
	})
}

// HandlePickedUp: ready → in_delivery.
func (s *Service) HandlePickedUp(ctx context.Context, eventID string, p events.DeliveryPayload) error {
	return s.applyEvent(ctx, eventID, events.DeliveryPickedUp, p.OrderID, func(o *domain.Order) ([]events.Envelope, error) {
		if o.Status != domain.StatusReady {
			if o.Status == domain.StatusAccepted {
				// Событие опередило order.ready — повторим позже.
				return nil, apperr.Precondition("order is not ready yet")
			}
			return nil, nil
		}
		if p.CourierID != "" {
			o.AssignCourier(p.CourierID, p.CourierName, s.now())
		}
		return s.transition(ctx, o, domain.StatusInDelivery, events.OrderInDelivery, "")
	})
}

// HandleDelivered: in_delivery → delivered.
func (s *Service) HandleDelivered(ctx context.Context, eventID string, p events.DeliveryPayload) error {
	return s.applyEvent(ctx, eventID, events.DeliveryDelivered, p.OrderID, func(o *domain.Order) ([]events.Envelope, error) {
		if o.Status != domain.StatusInDelivery {
			if o.Status == domain.StatusReady {
				return nil, apperr.Precondition("order is not in delivery yet")
			}
			return nil, nil
		}
		return s.transition(ctx, o, domain.StatusDelivered, events.OrderDelivered, "")
	})
}

// ---------------------------------------------------------- вспомогательное

func (s *Service) mutate(ctx context.Context, id string, fn MutateFunc) (*domain.Order, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, domain.ErrNotFound
	}
	o, err := s.repo.Update(ctx, id, fn)
	if err != nil {
		return nil, err
	}
	s.log.InfoContext(ctx, "order updated", slog.String("order_id", o.ID), slog.String("status", string(o.Status)))
	return o, nil
}

func (s *Service) applyEvent(ctx context.Context, eventID, eventType, orderID string, fn MutateFunc) error {
	if _, err := uuid.Parse(orderID); err != nil {
		return apperr.Invalid("event has invalid order id")
	}
	applied, err := s.repo.UpdateOnce(ctx, eventID, eventType, orderID, fn)
	if err != nil {
		return err
	}
	if !applied {
		s.log.InfoContext(ctx, "duplicate event skipped", slog.String("event_id", eventID), slog.String("type", eventType))
	}
	return nil
}

// transition переводит заказ в новый статус и формирует событие о переходе.
func (s *Service) transition(ctx context.Context, o *domain.Order, to domain.Status, eventType, reason string) ([]events.Envelope, error) {
	prev := o.Status
	if err := o.TransitionTo(to, reason, s.now()); err != nil {
		return nil, err
	}
	evt, err := s.event(ctx, eventType, o, prev, reason)
	if err != nil {
		return nil, err
	}
	return []events.Envelope{evt}, nil
}

func (s *Service) event(ctx context.Context, eventType string, o *domain.Order, prev domain.Status, reason string) (events.Envelope, error) {
	return events.New("order", eventType, requestid.From(ctx), events.OrderPayload{
		OrderID:           o.ID,
		CustomerID:        o.CustomerID,
		RestaurantID:      o.RestaurantID,
		RestaurantOwnerID: o.RestaurantOwnerID,
		RestaurantName:    o.RestaurantName,
		RestaurantAddress: o.RestaurantAddress,
		DeliveryAddress:   o.DeliveryAddress,
		Status:            string(o.Status),
		PreviousStatus:    string(prev),
		Total:             o.Total,
		ItemsCount:        o.ItemsCount(),
		CourierID:         o.CourierID,
		CourierName:       o.CourierName,
		Reason:            reason,
	})
}

func canView(actor auth.Identity, o *domain.Order) bool {
	switch {
	case actor.Privileged():
		return true
	case actor.Is(auth.RoleCustomer):
		return o.CustomerID == actor.UserID
	case actor.Is(auth.RoleRestaurant):
		return o.RestaurantOwnerID == actor.UserID
	case actor.Is(auth.RoleCourier):
		return o.CourierID == actor.UserID
	}
	return false
}

func isRestaurantOwner(actor auth.Identity, o *domain.Order) bool {
	return actor.Privileged() || (actor.Is(auth.RoleRestaurant) && o.RestaurantOwnerID == actor.UserID)
}
