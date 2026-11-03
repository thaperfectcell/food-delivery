// Package domain содержит доменную модель Delivery Service: курьеры, доставки,
// их статусы и правила переходов. Пакет не зависит от инфраструктуры.
package domain

import (
	"strings"
	"time"

	"github.com/thaperfectcell/food-delivery/pkg/apperr"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
)

// CourierStatus — статус курьера.
type CourierStatus string

const (
	CourierOffline   CourierStatus = "offline"   // не на смене
	CourierAvailable CourierStatus = "available" // на смене, свободен
	CourierBusy      CourierStatus = "busy"      // выполняет доставку
)

// Status — статус доставки.
type Status string

const (
	StatusPending   Status = "pending"   // ожидает свободного курьера
	StatusAssigned  Status = "assigned"  // курьер назначен
	StatusPickedUp  Status = "picked_up" // курьер забрал заказ
	StatusDelivered Status = "delivered" // доставлено
	StatusCancelled Status = "cancelled" // отменено
)

// IsActive — доставка выполняется курьером (назначена или в пути).
func (s Status) IsActive() bool { return s == StatusAssigned || s == StatusPickedUp }

// Ошибки доменного уровня.
var (
	ErrDeliveryNotFound = apperr.NotFound("доставка не найдена")
	ErrCourierNotFound  = apperr.NotFound("курьер не найден")
	ErrForbidden        = apperr.Forbidden("нет доступа к доставке")
	ErrNotYourDelivery  = apperr.Forbidden("доставка назначена другому курьеру")
	ErrCourierBusy      = apperr.Precondition("сначала завершите текущую доставку")
	ErrOrderNotReady    = apperr.Precondition("ресторан ещё не приготовил заказ")
	ErrNotAssigned      = apperr.Precondition("забрать можно только назначенный заказ")
	ErrNotPickedUp      = apperr.Precondition("завершить можно только доставку, которую курьер забрал")
	ErrCannotCancel     = apperr.Precondition("заказ уже у курьера — отменить доставку нельзя")
	ErrCannotAssign     = apperr.Precondition("назначение невозможно: доставка не ожидает курьера или курьер занят")
	// ErrDeliveryExists — доставка по заказу уже создана (в том числе параллельно).
	ErrDeliveryExists = apperr.Conflict("доставка по заказу уже существует")
)

// Courier — агрегат «Курьер» (идентификатор совпадает с идентификатором пользователя).
type Courier struct {
	ID                  string
	Name                string
	Status              CourierStatus
	CompletedDeliveries int
	StatusChangedAt     time.Time
	CreatedAt           time.Time
}

// NewCourier создаёт профиль курьера (по умолчанию — не на смене).
func NewCourier(id, name string, now time.Time) *Courier {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Курьер"
	}
	return &Courier{ID: id, Name: name, Status: CourierOffline, StatusChangedAt: now, CreatedAt: now}
}

// GoOnline — выход на смену. Занятый курьер остаётся занятым.
func (c *Courier) GoOnline(now time.Time) {
	if c.Status == CourierOffline {
		c.setStatus(CourierAvailable, now)
	}
}

// GoOffline — уход со смены (невозможен во время доставки).
func (c *Courier) GoOffline(now time.Time) error {
	switch c.Status {
	case CourierBusy:
		return ErrCourierBusy
	case CourierAvailable:
		c.setStatus(CourierOffline, now)
	}
	return nil
}

// Release освобождает курьера после завершения или отмены доставки.
func (c *Courier) Release(now time.Time) {
	if c.Status == CourierBusy {
		c.setStatus(CourierAvailable, now)
	}
}

func (c *Courier) setStatus(s CourierStatus, now time.Time) {
	c.Status = s
	c.StatusChangedAt = now
}

// Delivery — агрегат «Доставка» (одна доставка на заказ).
type Delivery struct {
	ID                string
	OrderID           string
	CustomerID        string
	CourierID         string
	CourierName       string
	Status            Status
	RestaurantName    string
	RestaurantAddress string
	DeliveryAddress   string
	OrderTotal        int64
	OrderReady        bool
	CreatedAt         time.Time
	AssignedAt        time.Time // нулевое значение — ещё не назначена
	PickedUpAt        time.Time
	DeliveredAt       time.Time
	CancelledAt       time.Time
}

// NewDeliveryParams — данные заказа для создания доставки.
type NewDeliveryParams struct {
	ID                string
	OrderID           string
	CustomerID        string
	RestaurantName    string
	RestaurantAddress string
	DeliveryAddress   string
	OrderTotal        int64
	Now               time.Time
}

// NewDelivery создаёт доставку, ожидающую назначения курьера.
func NewDelivery(p NewDeliveryParams) *Delivery {
	return &Delivery{
		ID:                p.ID,
		OrderID:           p.OrderID,
		CustomerID:        p.CustomerID,
		Status:            StatusPending,
		RestaurantName:    p.RestaurantName,
		RestaurantAddress: p.RestaurantAddress,
		DeliveryAddress:   p.DeliveryAddress,
		OrderTotal:        p.OrderTotal,
		CreatedAt:         p.Now,
	}
}

// NewCancelledDelivery создаёт «маркер» отменённой доставки для заказа,
// отменённого до её создания: запоздавшее событие order.accepted будет проигнорировано.
func NewCancelledDelivery(p NewDeliveryParams) *Delivery {
	d := NewDelivery(p)
	d.Status = StatusCancelled
	d.CancelledAt = p.Now
	return d
}

// Assign назначает свободного курьера на ожидающую доставку.
func (d *Delivery) Assign(c *Courier, now time.Time) error {
	if d.Status != StatusPending || c.Status != CourierAvailable {
		return ErrCannotAssign
	}
	d.Status = StatusAssigned
	d.CourierID = c.ID
	d.CourierName = c.Name
	d.AssignedAt = now
	c.setStatus(CourierBusy, now)
	return nil
}

// MarkReady фиксирует, что ресторан приготовил заказ.
func (d *Delivery) MarkReady() { d.OrderReady = true }

// PickUp — курьер забрал заказ в ресторане.
func (d *Delivery) PickUp(courierID string, now time.Time) error {
	if d.CourierID != courierID {
		return ErrNotYourDelivery
	}
	if d.Status != StatusAssigned {
		return ErrNotAssigned
	}
	if !d.OrderReady {
		return ErrOrderNotReady
	}
	d.Status = StatusPickedUp
	d.PickedUpAt = now
	return nil
}

// Complete — курьер передал заказ клиенту; курьер освобождается.
func (d *Delivery) Complete(courierID string, c *Courier, now time.Time) error {
	if d.CourierID != courierID {
		return ErrNotYourDelivery
	}
	if d.Status != StatusPickedUp {
		return ErrNotPickedUp
	}
	d.Status = StatusDelivered
	d.DeliveredAt = now
	if c != nil {
		c.Release(now)
		c.CompletedDeliveries++
	}
	return nil
}

// Cancel отменяет доставку, пока заказ не у курьера; назначенный курьер
// освобождается.
func (d *Delivery) Cancel(c *Courier, now time.Time) error {
	if d.Status != StatusPending && d.Status != StatusAssigned {
		return ErrCannotCancel
	}
	d.Status = StatusCancelled
	d.CancelledAt = now
	if c != nil && c.ID == d.CourierID {
		c.Release(now)
	}
	return nil
}

// CanView сообщает, может ли вызывающий видеть доставку: администратор/система,
// клиент — получатель заказа, назначенный курьер.
func (d *Delivery) CanView(actor auth.Identity) bool {
	switch {
	case actor.Privileged():
		return true
	case actor.Is(auth.RoleCustomer):
		return actor.UserID == d.CustomerID
	case actor.Is(auth.RoleCourier):
		return d.CourierID != "" && actor.UserID == d.CourierID
	}
	return false
}

// Stats — сводная статистика доставки.
type Stats struct {
	CouriersOnline      int // на смене (свободны или заняты)
	CouriersBusy        int
	DeliveriesPending   int
	DeliveriesActive    int // назначены или в пути
	DeliveriesCompleted int
}
