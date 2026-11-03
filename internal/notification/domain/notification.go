// Package domain содержит доменную модель Notification Service: уведомление и
// правила его формирования по доменным событиям других сервисов. Пакет не
// зависит от инфраструктуры; функция Compose — чистая и полностью покрыта тестами.
package domain

import (
	"time"

	"github.com/thaperfectcell/food-delivery/pkg/apperr"
)

// Notification — уведомление пользователя.
type Notification struct {
	ID        string
	UserID    string
	Type      string // тип исходного события, например order.accepted
	Title     string
	Body      string
	OrderID   string // пусто, если уведомление не относится к заказу
	CreatedAt time.Time
	ReadAt    time.Time // нулевое значение — не прочитано
}

// Read сообщает, прочитано ли уведомление.
func (n Notification) Read() bool { return !n.ReadAt.IsZero() }

// Draft — черновик уведомления, сформированный по событию (без id и времени).
type Draft struct {
	UserID  string
	Title   string
	Body    string
	OrderID string
}

// Ограничения запросов.
const (
	DefaultListLimit = 30
	MaxListLimit     = 100
	MaxMarkReadIDs   = 200
)

// Ошибки доменного уровня.
var (
	ErrNothingToMark = apperr.Invalid("укажите уведомления или флаг all")
	ErrTooManyIDs    = apperr.Invalid("слишком много уведомлений в одном запросе")
	ErrInvalidID     = apperr.Invalid("некорректный идентификатор уведомления")
)
