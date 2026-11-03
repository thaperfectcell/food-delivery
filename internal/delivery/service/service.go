// Package service — слой бизнес-логики Delivery Service: управление сменой
// курьеров, автоматическое назначение курьеров на доставки и выполнение доставки.
//
// Доставка создаётся по событию order.accepted (ресторан принял заказ), чтобы
// курьер успел приехать к моменту готовности. Назначение — по принципу FIFO:
// самая ранняя ожидающая доставка получает курьера, дольше всех ожидающего
// заказа. Конкурентная безопасность обеспечивается блокировками строк в БД
// (FOR UPDATE SKIP LOCKED), поэтому сервис можно масштабировать горизонтально.
package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/thaperfectcell/food-delivery/internal/delivery/domain"
	"github.com/thaperfectcell/food-delivery/pkg/apperr"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
	"github.com/thaperfectcell/food-delivery/pkg/events"
	"github.com/thaperfectcell/food-delivery/pkg/requestid"
)

// DeliveryMutateFunc изменяет заблокированную доставку и её курьера (c == nil,
// если курьер не назначен) и возвращает события для outbox.
type DeliveryMutateFunc func(d *domain.Delivery, c *domain.Courier) ([]events.Envelope, error)

// CourierMutateFunc изменяет заблокированного курьера.
type CourierMutateFunc func(c *domain.Courier) error

// Repository — порт хранилища курьеров и доставок.
type Repository interface {
	GetCourier(ctx context.Context, id string) (*domain.Courier, error)
	// UpsertCourier создаёт профиль курьера при первом обращении (имя обновляется,
	// если передано непустым), блокирует его и применяет fn в одной транзакции.
	UpsertCourier(ctx context.Context, id, name string, now time.Time, fn CourierMutateFunc) (*domain.Courier, error)

	GetDelivery(ctx context.Context, id string) (*domain.Delivery, error)
	GetDeliveryByOrder(ctx context.Context, orderID string) (*domain.Delivery, error)
	ListCourierDeliveries(ctx context.Context, courierID string, activeOnly bool, limit int) ([]*domain.Delivery, error)

	// CreateOnce отмечает событие обработанным и сохраняет доставку в одной
	// транзакции. false без ошибки — событие уже обработано;
	// domain.ErrDeliveryExists — доставка по заказу уже существует (откат).
	CreateOnce(ctx context.Context, eventID, eventType string, d *domain.Delivery) (bool, error)
	// UpdateDelivery блокирует доставку (и назначенного курьера), применяет fn и
	// атомарно сохраняет изменения и события.
	UpdateDelivery(ctx context.Context, id string, fn DeliveryMutateFunc) (*domain.Delivery, error)
	// UpdateByOrderOnce — то же по идентификатору заказа, не более одного раза
	// для события eventID.
	UpdateByOrderOnce(ctx context.Context, eventID, eventType, orderID string, fn DeliveryMutateFunc) (bool, error)
	// AssignNext блокирует самую раннюю ожидающую доставку и свободного курьера
	// (дольше всех ожидающего), применяет fn и сохраняет результат.
	// false — подходящей пары нет.
	AssignNext(ctx context.Context, fn DeliveryMutateFunc) (bool, error)

	Stats(ctx context.Context) (domain.Stats, error)
}

// maxAssignmentsPerRun ограничивает число назначений за один проход.
const maxAssignmentsPerRun = 100

// Service реализует сценарии Delivery Service.
type Service struct {
	repo  Repository
	log   *slog.Logger
	now   func() time.Time
	newID func() string
}

// New создаёт сервис.
func New(repo Repository, log *slog.Logger) *Service {
	return &Service{
		repo:  repo,
		log:   log,
		now:   func() time.Time { return time.Now().UTC() },
		newID: uuid.NewString,
	}
}

// ---------------------------------------------------------- курьер

// SetCourierStatus — выход курьера на смену (online=true) или уход со смены.
// После выхода на смену курьеру сразу может быть назначен ожидающий заказ.
func (s *Service) SetCourierStatus(ctx context.Context, actor auth.Identity, online bool, name string) (*domain.Courier, error) {
	if !actor.Is(auth.RoleCourier) {
		return nil, apperr.Forbidden("доступно только курьерам")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		// Имя не передано: для нового профиля берём имя из токена, у существующего
		// профиля сохраняем прежнее.
		if _, err := s.repo.GetCourier(ctx, actor.UserID); errors.Is(err, domain.ErrCourierNotFound) {
			name = strings.TrimSpace(actor.Name)
		}
	}
	if utf8.RuneCountInString(name) > 100 {
		name = string([]rune(name)[:100])
	}
	now := s.now()
	c, err := s.repo.UpsertCourier(ctx, actor.UserID, name, now, func(c *domain.Courier) error {
		if online {
			c.GoOnline(now)
			return nil
		}
		return c.GoOffline(now)
	})
	if err != nil {
		return nil, err
	}
	s.log.InfoContext(ctx, "courier status changed", slog.String("courier_id", c.ID), slog.String("status", string(c.Status)))

	if c.Status == domain.CourierAvailable {
		s.assignSafely(ctx)
		if fresh, err := s.repo.GetCourier(ctx, c.ID); err == nil {
			c = fresh // курьер мог сразу получить заказ
		}
	}
	return c, nil
}

// GetCourier возвращает профиль курьера: свой (id пустой) или любой — для
// администратора. Курьер, ещё ни разу не выходивший на смену, считается offline.
func (s *Service) GetCourier(ctx context.Context, actor auth.Identity, id string) (*domain.Courier, error) {
	id = strings.TrimSpace(id)
	if id == "" || id == actor.UserID {
		if !actor.Is(auth.RoleCourier) {
			return nil, apperr.Forbidden("доступно только курьерам")
		}
		c, err := s.repo.GetCourier(ctx, actor.UserID)
		if errors.Is(err, domain.ErrCourierNotFound) {
			return domain.NewCourier(actor.UserID, actor.Name, s.now()), nil
		}
		return c, err
	}
	if !actor.Privileged() {
		return nil, apperr.Forbidden("нет доступа к профилю курьера")
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, domain.ErrCourierNotFound
	}
	return s.repo.GetCourier(ctx, id)
}

// ListCourierDeliveries — доставки текущего курьера (activeOnly — назначенные и в пути).
func (s *Service) ListCourierDeliveries(ctx context.Context, actor auth.Identity, activeOnly bool, limit int) ([]*domain.Delivery, error) {
	if !actor.Is(auth.RoleCourier) {
		return nil, apperr.Forbidden("доступно только курьерам")
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.repo.ListCourierDeliveries(ctx, actor.UserID, activeOnly, limit)
}

// PickUp — курьер забрал заказ в ресторане (заказ должен быть готов).
func (s *Service) PickUp(ctx context.Context, actor auth.Identity, id string) (*domain.Delivery, error) {
	if !actor.Is(auth.RoleCourier) {
		return nil, apperr.Forbidden("доступно только курьерам")
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, domain.ErrDeliveryNotFound
	}
	d, err := s.repo.UpdateDelivery(ctx, id, func(d *domain.Delivery, _ *domain.Courier) ([]events.Envelope, error) {
		if err := d.PickUp(actor.UserID, s.now()); err != nil {
			return nil, err
		}
		return s.deliveryEvent(ctx, events.DeliveryPickedUp, d, "")
	})
	if err != nil {
		return nil, err
	}
	s.log.InfoContext(ctx, "order picked up", slog.String("delivery_id", d.ID), slog.String("order_id", d.OrderID))
	return d, nil
}

// Complete — курьер передал заказ клиенту. Курьер освобождается и может сразу
// получить следующий заказ.
func (s *Service) Complete(ctx context.Context, actor auth.Identity, id string) (*domain.Delivery, error) {
	if !actor.Is(auth.RoleCourier) {
		return nil, apperr.Forbidden("доступно только курьерам")
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, domain.ErrDeliveryNotFound
	}
	d, err := s.repo.UpdateDelivery(ctx, id, func(d *domain.Delivery, c *domain.Courier) ([]events.Envelope, error) {
		if err := d.Complete(actor.UserID, c, s.now()); err != nil {
			return nil, err
		}
		return s.deliveryEvent(ctx, events.DeliveryDelivered, d, "")
	})
	if err != nil {
		return nil, err
	}
	s.log.InfoContext(ctx, "order delivered", slog.String("delivery_id", d.ID), slog.String("order_id", d.OrderID))
	s.assignSafely(ctx)
	return d, nil
}

// ---------------------------------------------------------- запросы

// GetDeliveryByOrder возвращает доставку заказа (администратор/система, клиент
// заказа или назначенный курьер).
func (s *Service) GetDeliveryByOrder(ctx context.Context, actor auth.Identity, orderID string) (*domain.Delivery, error) {
	if actor.IsZero() {
		return nil, apperr.Unauthenticated("требуется вход в систему")
	}
	if _, err := uuid.Parse(orderID); err != nil {
		return nil, domain.ErrDeliveryNotFound
	}
	d, err := s.repo.GetDeliveryByOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if !d.CanView(actor) {
		return nil, domain.ErrForbidden
	}
	return d, nil
}

// Stats — статистика доставки (администратор или система).
func (s *Service) Stats(ctx context.Context, actor auth.Identity) (domain.Stats, error) {
	if !actor.Privileged() {
		return domain.Stats{}, apperr.Forbidden("статистика доступна только администратору")
	}
	return s.repo.Stats(ctx)
}

// ---------------------------------------------------------- реакции на события

// HandleOrderAccepted создаёт доставку для принятого рестораном заказа и
// запускает назначение курьера.
func (s *Service) HandleOrderAccepted(ctx context.Context, eventID string, o events.OrderPayload) error {
	if err := validateOrder(o); err != nil {
		return err
	}
	d := domain.NewDelivery(s.params(o))
	created, err := s.repo.CreateOnce(ctx, eventID, events.OrderAccepted, d)
	switch {
	case errors.Is(err, domain.ErrDeliveryExists):
		// Повтор события или заказ уже отменён (есть маркер отмены) — ничего не создаём.
		s.log.InfoContext(ctx, "delivery already exists, order.accepted skipped", slog.String("order_id", o.OrderID))
	case err != nil:
		return err
	case created:
		s.log.InfoContext(ctx, "delivery created", slog.String("delivery_id", d.ID), slog.String("order_id", o.OrderID))
	}
	return s.AssignPending(ctx)
}

// HandleOrderReady отмечает, что ресторан приготовил заказ — курьер может его забрать.
func (s *Service) HandleOrderReady(ctx context.Context, eventID string, o events.OrderPayload) error {
	if err := validateOrder(o); err != nil {
		return err
	}
	_, err := s.repo.UpdateByOrderOnce(ctx, eventID, events.OrderReady, o.OrderID,
		func(d *domain.Delivery, _ *domain.Courier) ([]events.Envelope, error) {
			d.MarkReady()
			return nil, nil
		})
	if errors.Is(err, domain.ErrDeliveryNotFound) {
		// order.ready опередило order.accepted (повторная доставка) — повторим позже.
		return apperr.Wrap(apperr.KindUnavailable, "доставка по заказу ещё не создана", err)
	}
	return err
}

// HandleOrderCancelled отменяет доставку отменённого заказа и освобождает курьера.
// Если доставки ещё нет, сохраняется маркер отмены, чтобы запоздавшее
// order.accepted не создало доставку для отменённого заказа.
func (s *Service) HandleOrderCancelled(ctx context.Context, eventID string, o events.OrderPayload) error {
	if err := validateOrder(o); err != nil {
		return err
	}
	if _, err := s.repo.GetDeliveryByOrder(ctx, o.OrderID); errors.Is(err, domain.ErrDeliveryNotFound) {
		marker := domain.NewCancelledDelivery(s.params(o))
		_, err := s.repo.CreateOnce(ctx, eventID, events.OrderCancelled, marker)
		if errors.Is(err, domain.ErrDeliveryExists) {
			return apperr.Wrap(apperr.KindConflict, "доставка создана параллельно, требуется повтор", err)
		}
		return err
	} else if err != nil {
		return err
	}

	applied, err := s.repo.UpdateByOrderOnce(ctx, eventID, events.OrderCancelled, o.OrderID,
		func(d *domain.Delivery, c *domain.Courier) ([]events.Envelope, error) {
			if d.Status != domain.StatusPending && d.Status != domain.StatusAssigned {
				return nil, nil // уже отменена или заказ у курьера
			}
			if err := d.Cancel(c, s.now()); err != nil {
				return nil, err
			}
			return s.deliveryEvent(ctx, events.DeliveryCancelled, d, o.Reason)
		})
	if err != nil {
		return err
	}
	if applied {
		s.log.InfoContext(ctx, "delivery cancelled", slog.String("order_id", o.OrderID))
		return s.AssignPending(ctx) // освободившийся курьер может взять следующий заказ
	}
	return nil
}

// ---------------------------------------------------------- назначение курьеров

// AssignPending назначает свободных курьеров на ожидающие доставки, пока есть пары.
func (s *Service) AssignPending(ctx context.Context) error {
	for i := 0; i < maxAssignmentsPerRun; i++ {
		var assigned *domain.Delivery
		ok, err := s.repo.AssignNext(ctx, func(d *domain.Delivery, c *domain.Courier) ([]events.Envelope, error) {
			if err := d.Assign(c, s.now()); err != nil {
				return nil, err
			}
			assigned = d
			return s.deliveryEvent(ctx, events.DeliveryCourierAssigned, d, "")
		})
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		s.log.InfoContext(ctx, "courier assigned",
			slog.String("delivery_id", assigned.ID), slog.String("order_id", assigned.OrderID),
			slog.String("courier_id", assigned.CourierID))
	}
	return nil
}

// RunAssignmentLoop периодически запускает назначение — страховка на случай,
// если событие-триггер было обработано с ошибкой.
func (s *Service) RunAssignmentLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.AssignPending(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("periodic assignment failed", slog.String("error", err.Error()))
			}
		}
	}
}

// assignSafely запускает назначение после успешной пользовательской операции;
// ошибка не влияет на её результат (повторит периодический цикл).
func (s *Service) assignSafely(ctx context.Context) {
	if err := s.AssignPending(ctx); err != nil {
		s.log.WarnContext(ctx, "assignment failed", slog.String("error", err.Error()))
	}
}

// ---------------------------------------------------------- вспомогательное

func validateOrder(o events.OrderPayload) error {
	if _, err := uuid.Parse(o.OrderID); err != nil {
		return apperr.Invalid("событие содержит некорректный идентификатор заказа")
	}
	if _, err := uuid.Parse(o.CustomerID); err != nil {
		return apperr.Invalid("событие содержит некорректный идентификатор клиента")
	}
	return nil
}

func (s *Service) params(o events.OrderPayload) domain.NewDeliveryParams {
	return domain.NewDeliveryParams{
		ID:                s.newID(),
		OrderID:           o.OrderID,
		CustomerID:        o.CustomerID,
		RestaurantName:    o.RestaurantName,
		RestaurantAddress: o.RestaurantAddress,
		DeliveryAddress:   o.DeliveryAddress,
		OrderTotal:        o.Total,
		Now:               s.now(),
	}
}

// deliveryEvent формирует событие о доставке (одно событие в срезе — для outbox).
func (s *Service) deliveryEvent(ctx context.Context, eventType string, d *domain.Delivery, reason string) ([]events.Envelope, error) {
	e, err := events.New("delivery", eventType, requestid.From(ctx), events.DeliveryPayload{
		DeliveryID:        d.ID,
		OrderID:           d.OrderID,
		CustomerID:        d.CustomerID,
		CourierID:         d.CourierID,
		CourierName:       d.CourierName,
		Status:            string(d.Status),
		RestaurantName:    d.RestaurantName,
		RestaurantAddress: d.RestaurantAddress,
		DeliveryAddress:   d.DeliveryAddress,
		Reason:            reason,
	})
	if err != nil {
		return nil, err
	}
	return []events.Envelope{e}, nil
}
