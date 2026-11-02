// Package domain содержит доменную модель Payment Service: платёж, его статусы
// и правила переходов. Пакет не зависит от инфраструктуры.
package domain

import (
	"time"

	"github.com/thaperfectcell/food-delivery/pkg/apperr"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
)

// Status — статус платежа.
type Status string

const (
	StatusPending   Status = "pending"   // платёж в обработке у провайдера
	StatusSucceeded Status = "succeeded" // средства списаны
	StatusFailed    Status = "failed"    // платёж отклонён
	StatusRefunded  Status = "refunded"  // средства возвращены клиенту
)

// Valid сообщает, известен ли статус.
func (s Status) Valid() bool {
	switch s {
	case StatusPending, StatusSucceeded, StatusFailed, StatusRefunded:
		return true
	}
	return false
}

// Причины отказа, формируемые самим сервисом.
const (
	ReasonCancelledBeforePayment = "заказ отменён до оплаты"
)

// Ошибки доменного уровня.
var (
	ErrNotFound      = apperr.NotFound("платёж не найден")
	ErrForbidden     = apperr.Forbidden("нет доступа к платежу")
	ErrNotRefundable = apperr.Precondition("вернуть можно только успешно проведённый платёж")
	// ErrPaymentExists — платёж по заказу уже создан (в том числе параллельно
	// другим экземпляром сервиса); сохранение нового платежа не выполняется.
	ErrPaymentExists = apperr.Conflict("платёж по заказу уже существует")
)

// Payment — агрегат «Платёж». На один заказ приходится не более одного платежа
// (уникальность order_id обеспечивается и в БД).
type Payment struct {
	ID            string
	OrderID       string
	CustomerID    string
	Amount        int64 // копейки
	Status        Status
	FailureReason string
	ProviderRef   string // идентификатор транзакции у платёжного провайдера
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// NewSucceeded создаёт успешно проведённый платёж.
func NewSucceeded(id, orderID, customerID string, amount int64, providerRef string, now time.Time) *Payment {
	return &Payment{
		ID: id, OrderID: orderID, CustomerID: customerID, Amount: amount,
		Status: StatusSucceeded, ProviderRef: providerRef, CreatedAt: now, UpdatedAt: now,
	}
}

// NewFailed создаёт отклонённый платёж с причиной отказа.
func NewFailed(id, orderID, customerID string, amount int64, reason string, now time.Time) *Payment {
	return &Payment{
		ID: id, OrderID: orderID, CustomerID: customerID, Amount: amount,
		Status: StatusFailed, FailureReason: reason, CreatedAt: now, UpdatedAt: now,
	}
}

// Refund переводит успешный платёж в статус «возвращён».
func (p *Payment) Refund(now time.Time) error {
	if p.Status != StatusSucceeded {
		return ErrNotRefundable
	}
	p.Status = StatusRefunded
	p.UpdatedAt = now
	return nil
}

// CanView сообщает, может ли вызывающий просматривать платёж:
// администратор/система или клиент — владелец платежа.
func (p *Payment) CanView(actor auth.Identity) bool {
	if actor.Privileged() {
		return true
	}
	return actor.Is(auth.RoleCustomer) && actor.UserID == p.CustomerID
}

// ChargeResult — ответ платёжного провайдера на запрос списания.
type ChargeResult struct {
	Approved      bool
	ProviderRef   string
	DeclineReason string
}
