// Package service — слой бизнес-логики Payment Service.
//
// Платёж проводится асинхронно, как шаг хореографической саги оформления
// заказа: по событию order.created сервис обращается к платёжному провайдеру
// и публикует результат (payment.succeeded / payment.failed); по событию
// order.cancelled выполняет компенсирующее действие — возврат средств.
package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/thaperfectcell/food-delivery/internal/payment/domain"
	"github.com/thaperfectcell/food-delivery/pkg/apperr"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
	"github.com/thaperfectcell/food-delivery/pkg/events"
	"github.com/thaperfectcell/food-delivery/pkg/requestid"
)

// MutateFunc изменяет заблокированный платёж и возвращает события для outbox.
type MutateFunc func(p *domain.Payment) ([]events.Envelope, error)

// Repository — порт хранилища платежей.
type Repository interface {
	GetByOrder(ctx context.Context, orderID string) (*domain.Payment, error)
	List(ctx context.Context, limit, offset int) ([]*domain.Payment, int, error)
	// IsProcessed сообщает, было ли событие уже обработано.
	IsProcessed(ctx context.Context, eventID string) (bool, error)
	// CreateOnce в одной транзакции отмечает событие обработанным, сохраняет
	// платёж и события outbox. Возвращает false без ошибки, если событие уже
	// обработано, и domain.ErrPaymentExists, если платёж по заказу уже есть
	// (в этом случае транзакция откатывается целиком).
	CreateOnce(ctx context.Context, eventID, eventType string, p *domain.Payment, evts []events.Envelope) (bool, error)
	// UpdateOnce блокирует платёж заказа (SELECT … FOR UPDATE), применяет fn
	// и атомарно сохраняет изменения, события и отметку об обработке события.
	UpdateOnce(ctx context.Context, eventID, eventType, orderID string, fn MutateFunc) (bool, error)
}

// Provider — порт платёжного провайдера (эквайринга). Обращение к провайдеру
// выполняется вне транзакции БД; провайдер идемпотентен по ключу.
type Provider interface {
	// Charge списывает amount копеек; idempotencyKey — идентификатор заказа.
	Charge(ctx context.Context, idempotencyKey string, amount int64) (domain.ChargeResult, error)
	// Refund возвращает средства по транзакции providerRef.
	Refund(ctx context.Context, providerRef string, amount int64) error
}

// Service реализует сценарии Payment Service.
type Service struct {
	repo     Repository
	provider Provider
	log      *slog.Logger
	now      func() time.Time
	newID    func() string
}

// New создаёт сервис.
func New(repo Repository, provider Provider, log *slog.Logger) *Service {
	return &Service{
		repo:     repo,
		provider: provider,
		log:      log,
		now:      func() time.Time { return time.Now().UTC() },
		newID:    uuid.NewString,
	}
}

// ---------------------------------------------------------- реакции на события

// HandleOrderCreated проводит оплату нового заказа.
//
// Порядок действий: (1) проверка, что платёж по заказу ещё не создан и событие
// не обработано; (2) вызов провайдера вне транзакции (провайдер идемпотентен по
// идентификатору заказа, поэтому повторная доставка события не приведёт к
// двойному списанию); (3) одна транзакция: отметка события, платёж и событие
// payment.succeeded/payment.failed в outbox.
func (s *Service) HandleOrderCreated(ctx context.Context, eventID string, o events.OrderPayload) error {
	if err := validateOrder(o); err != nil {
		return err
	}
	if _, err := s.repo.GetByOrder(ctx, o.OrderID); err == nil {
		s.log.InfoContext(ctx, "payment already exists, order.created skipped", slog.String("order_id", o.OrderID))
		return nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	if done, err := s.repo.IsProcessed(ctx, eventID); err != nil {
		return err
	} else if done {
		return nil
	}

	res, err := s.provider.Charge(ctx, o.OrderID, o.Total)
	if err != nil {
		return apperr.Wrap(apperr.KindUnavailable, "платёжный провайдер недоступен", err)
	}

	now := s.now()
	var (
		p         *domain.Payment
		eventType string
	)
	if res.Approved {
		p = domain.NewSucceeded(s.newID(), o.OrderID, o.CustomerID, o.Total, res.ProviderRef, now)
		eventType = events.PaymentSucceeded
	} else {
		p = domain.NewFailed(s.newID(), o.OrderID, o.CustomerID, o.Total, res.DeclineReason, now)
		eventType = events.PaymentFailed
	}
	evt, err := s.event(ctx, eventType, p, p.FailureReason)
	if err != nil {
		return err
	}

	created, err := s.repo.CreateOnce(ctx, eventID, events.OrderCreated, p, []events.Envelope{evt})
	if errors.Is(err, domain.ErrPaymentExists) {
		// Платёж по заказу параллельно создан другим обработчиком (например,
		// заказ отменён до оплаты). Списание отменяем у провайдера.
		if res.Approved {
			if rerr := s.provider.Refund(ctx, res.ProviderRef, o.Total); rerr != nil {
				return apperr.Wrap(apperr.KindUnavailable, "не удалось отменить списание", rerr)
			}
		}
		s.log.WarnContext(ctx, "payment for order created concurrently, charge voided", slog.String("order_id", o.OrderID))
		return nil
	}
	if err != nil {
		return err
	}
	if created {
		s.log.InfoContext(ctx, "payment processed",
			slog.String("order_id", o.OrderID), slog.String("status", string(p.Status)),
			slog.Int64("amount", p.Amount), slog.String("reason", p.FailureReason))
	}
	return nil
}

// HandleOrderCancelled выполняет компенсацию при отмене заказа:
//   - платёж проведён → возврат средств и событие payment.refunded;
//   - платежа ещё нет → сохраняется отклонённый платёж «заказ отменён до оплаты»,
//     чтобы запоздавшее событие order.created не привело к списанию;
//   - платёж отклонён или уже возвращён → действий не требуется.
func (s *Service) HandleOrderCancelled(ctx context.Context, eventID string, o events.OrderPayload) error {
	if err := validateOrder(o); err != nil {
		return err
	}
	p, err := s.repo.GetByOrder(ctx, o.OrderID)
	if errors.Is(err, domain.ErrNotFound) {
		marker := domain.NewFailed(s.newID(), o.OrderID, o.CustomerID, o.Total, domain.ReasonCancelledBeforePayment, s.now())
		_, err := s.repo.CreateOnce(ctx, eventID, events.OrderCancelled, marker, nil)
		if errors.Is(err, domain.ErrPaymentExists) {
			// Платёж появился параллельно — повторим обработку, чтобы вернуть средства.
			return apperr.Wrap(apperr.KindConflict, "платёж создан параллельно, требуется повтор", err)
		}
		if err == nil {
			s.log.InfoContext(ctx, "order cancelled before payment", slog.String("order_id", o.OrderID))
		}
		return err
	}
	if err != nil {
		return err
	}
	if p.Status != domain.StatusSucceeded {
		return nil // возвращать нечего
	}

	// Возврат у провайдера (идемпотентен по ссылке на транзакцию) — вне транзакции БД.
	if err := s.provider.Refund(ctx, p.ProviderRef, p.Amount); err != nil {
		return apperr.Wrap(apperr.KindUnavailable, "платёжный провайдер недоступен", err)
	}
	reason := o.Reason
	applied, err := s.repo.UpdateOnce(ctx, eventID, events.OrderCancelled, o.OrderID, func(p *domain.Payment) ([]events.Envelope, error) {
		if p.Status != domain.StatusSucceeded {
			return nil, nil
		}
		if err := p.Refund(s.now()); err != nil {
			return nil, err
		}
		evt, err := s.event(ctx, events.PaymentRefunded, p, reason)
		if err != nil {
			return nil, err
		}
		return []events.Envelope{evt}, nil
	})
	if err != nil {
		return err
	}
	if applied {
		s.log.InfoContext(ctx, "payment refunded", slog.String("order_id", o.OrderID), slog.Int64("amount", p.Amount))
	}
	return nil
}

// ---------------------------------------------------------- запросы

// GetPaymentByOrder возвращает платёж по заказу (администратор, система или
// клиент — владелец платежа).
func (s *Service) GetPaymentByOrder(ctx context.Context, actor auth.Identity, orderID string) (*domain.Payment, error) {
	if actor.IsZero() {
		return nil, apperr.Unauthenticated("требуется вход в систему")
	}
	if _, err := uuid.Parse(orderID); err != nil {
		return nil, domain.ErrNotFound
	}
	p, err := s.repo.GetByOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if !p.CanView(actor) {
		return nil, domain.ErrForbidden
	}
	return p, nil
}

// ListPayments — журнал платежей (администратор или система).
func (s *Service) ListPayments(ctx context.Context, actor auth.Identity, limit, offset int) ([]*domain.Payment, int, error) {
	if !actor.Privileged() {
		return nil, 0, apperr.Forbidden("журнал платежей доступен только администратору")
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.List(ctx, limit, offset)
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

// event формирует событие о платеже; reason — причина отказа или возврата.
func (s *Service) event(ctx context.Context, eventType string, p *domain.Payment, reason string) (events.Envelope, error) {
	return events.New("payment", eventType, requestid.From(ctx), events.PaymentPayload{
		PaymentID:  p.ID,
		OrderID:    p.OrderID,
		CustomerID: p.CustomerID,
		Amount:     p.Amount,
		Status:     string(p.Status),
		Reason:     reason,
	})
}
