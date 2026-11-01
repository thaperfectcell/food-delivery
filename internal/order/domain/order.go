// Package domain содержит доменную модель заказа: сущности, бизнес-правила
// расчёта стоимости и конечный автомат статусов. Пакет не зависит от
// инфраструктуры (БД, брокера, gRPC) и полностью покрывается unit-тестами.
package domain

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/thaperfectcell/food-delivery/pkg/apperr"
)

// Status — статус заказа.
type Status string

const (
	StatusCreated    Status = "created"     // создан, ожидает оплаты
	StatusPaid       Status = "paid"        // оплачен, ожидает подтверждения ресторана
	StatusAccepted   Status = "accepted"    // принят рестораном, готовится
	StatusReady      Status = "ready"       // готов, ожидает курьера
	StatusInDelivery Status = "in_delivery" // передан курьеру
	StatusDelivered  Status = "delivered"   // доставлен
	StatusCancelled  Status = "cancelled"   // отменён
)

// AllStatuses — все статусы в порядке жизненного цикла.
var AllStatuses = []Status{
	StatusCreated, StatusPaid, StatusAccepted, StatusReady,
	StatusInDelivery, StatusDelivered, StatusCancelled,
}

// transitions — допустимые переходы конечного автомата.
var transitions = map[Status][]Status{
	StatusCreated:    {StatusPaid, StatusCancelled},
	StatusPaid:       {StatusAccepted, StatusCancelled},
	StatusAccepted:   {StatusReady, StatusCancelled},
	StatusReady:      {StatusInDelivery},
	StatusInDelivery: {StatusDelivered},
}

// Valid сообщает, известен ли статус.
func (s Status) Valid() bool {
	for _, x := range AllStatuses {
		if x == s {
			return true
		}
	}
	return false
}

// CanTransitionTo сообщает, допустим ли переход в статус next.
func (s Status) CanTransitionTo(next Status) bool {
	for _, n := range transitions[s] {
		if n == next {
			return true
		}
	}
	return false
}

// IsTerminal сообщает, является ли статус конечным.
func (s Status) IsTerminal() bool {
	return s == StatusDelivered || s == StatusCancelled
}

// Title — название статуса для пользователя.
func (s Status) Title() string {
	switch s {
	case StatusCreated:
		return "ожидает оплаты"
	case StatusPaid:
		return "оплачен"
	case StatusAccepted:
		return "готовится"
	case StatusReady:
		return "готов к выдаче"
	case StatusInDelivery:
		return "в пути"
	case StatusDelivered:
		return "доставлен"
	case StatusCancelled:
		return "отменён"
	}
	return string(s)
}

// Бизнес-правила оформления заказа (суммы — в копейках).
const (
	MinOrderAmount        int64 = 300_00   // минимальная сумма заказа — 300 ₽
	DeliveryFee           int64 = 99_00    // стоимость доставки — 99 ₽
	FreeDeliveryThreshold int64 = 1_500_00 // бесплатная доставка от 1500 ₽
	MaxItemQuantity             = 50       // максимум единиц одной позиции
	MaxPositions                = 30       // максимум различных позиций в заказе
	MaxAddressLength            = 300
	MaxCommentLength            = 500
)

// CalcDeliveryFee рассчитывает стоимость доставки по сумме позиций.
func CalcDeliveryFee(itemsTotal int64) int64 {
	if itemsTotal >= FreeDeliveryThreshold {
		return 0
	}
	return DeliveryFee
}

// Item — позиция заказа (снимок названия и цены на момент оформления).
type Item struct {
	MenuItemID string
	Name       string
	Price      int64
	Quantity   int32
}

// Total — стоимость позиции.
func (i Item) Total() int64 { return i.Price * int64(i.Quantity) }

// StatusChange — запись истории статусов.
type StatusChange struct {
	Status Status
	Reason string
	At     time.Time
}

// Order — агрегат «Заказ».
type Order struct {
	ID                string
	CustomerID        string
	RestaurantID      string
	RestaurantOwnerID string
	RestaurantName    string
	RestaurantAddress string
	Items             []Item
	ItemsTotal        int64
	DeliveryFee       int64
	Total             int64
	Status            Status
	DeliveryAddress   string
	Comment           string
	CourierID         string
	CourierName       string
	CancelReason      string
	IdempotencyKey    string
	History           []StatusChange
	CreatedAt         time.Time
	UpdatedAt         time.Time

	pending []StatusChange // изменения статуса, ещё не сохранённые в БД
}

// Restaurant — сведения о ресторане из каталога, нужные для оформления заказа.
type Restaurant struct {
	ID      string
	OwnerID string
	Name    string
	Address string
	Active  bool
}

// MenuItem — позиция меню из каталога (актуальная цена и доступность).
type MenuItem struct {
	ID        string
	Name      string
	Price     int64
	Available bool
}

// Line — запрошенная клиентом позиция.
type Line struct {
	MenuItemID string
	Quantity   int32
}

// NewOrderParams — входные данные для создания заказа.
type NewOrderParams struct {
	ID              string
	CustomerID      string
	Restaurant      Restaurant
	Lines           []Line
	Menu            map[string]MenuItem // позиции каталога по ID
	DeliveryAddress string
	Comment         string
	IdempotencyKey  string
	Now             time.Time
}

// Ошибки доменного уровня.
var (
	ErrNotFound          = apperr.NotFound("заказ не найден")
	ErrEmptyOrder        = apperr.Invalid("заказ должен содержать хотя бы одну позицию")
	ErrTooManyPositions  = apperr.Invalid("слишком много позиций в заказе")
	ErrInvalidQuantity   = apperr.Invalid("количество должно быть от 1 до 50")
	ErrAddressRequired   = apperr.Invalid("не указан адрес доставки")
	ErrAddressTooLong    = apperr.Invalid("адрес доставки слишком длинный")
	ErrCommentTooLong    = apperr.Invalid("комментарий слишком длинный")
	ErrRestaurantClosed  = apperr.Precondition("ресторан сейчас не принимает заказы")
	ErrItemNotFound      = apperr.Invalid("позиция меню не найдена в выбранном ресторане")
	ErrItemUnavailable   = apperr.Precondition("одна из позиций сейчас недоступна для заказа")
	ErrBelowMinimum      = apperr.Precondition("минимальная сумма заказа — 300 ₽")
	ErrForbidden         = apperr.Forbidden("нет доступа к заказу")
	ErrInvalidTransition = apperr.Precondition("операция недоступна в текущем статусе заказа")
)

// NewOrder создаёт заказ, проверяя бизнес-правила и рассчитывая стоимость
// строго по данным каталога (цены от клиента не принимаются).
func NewOrder(p NewOrderParams) (*Order, error) {
	if len(p.Lines) == 0 {
		return nil, ErrEmptyOrder
	}
	if len(p.Lines) > MaxPositions {
		return nil, ErrTooManyPositions
	}
	address := strings.TrimSpace(p.DeliveryAddress)
	if address == "" {
		return nil, ErrAddressRequired
	}
	if utf8.RuneCountInString(address) > MaxAddressLength {
		return nil, ErrAddressTooLong
	}
	comment := strings.TrimSpace(p.Comment)
	if utf8.RuneCountInString(comment) > MaxCommentLength {
		return nil, ErrCommentTooLong
	}
	if !p.Restaurant.Active {
		return nil, ErrRestaurantClosed
	}

	// Объединяем повторяющиеся позиции, сохраняя порядок.
	qty := make(map[string]int32, len(p.Lines))
	order := make([]string, 0, len(p.Lines))
	for _, l := range p.Lines {
		if l.Quantity < 1 || l.Quantity > MaxItemQuantity {
			return nil, ErrInvalidQuantity
		}
		if _, seen := qty[l.MenuItemID]; !seen {
			order = append(order, l.MenuItemID)
		}
		qty[l.MenuItemID] += l.Quantity
		if qty[l.MenuItemID] > MaxItemQuantity {
			return nil, ErrInvalidQuantity
		}
	}

	items := make([]Item, 0, len(order))
	var itemsTotal int64
	for _, id := range order {
		m, ok := p.Menu[id]
		if !ok {
			return nil, ErrItemNotFound
		}
		if !m.Available {
			return nil, apperr.Newf(apperr.KindPrecondition, "позиция «%s» сейчас недоступна", m.Name)
		}
		it := Item{MenuItemID: m.ID, Name: m.Name, Price: m.Price, Quantity: qty[id]}
		items = append(items, it)
		itemsTotal += it.Total()
	}
	if itemsTotal < MinOrderAmount {
		return nil, ErrBelowMinimum
	}
	fee := CalcDeliveryFee(itemsTotal)

	o := &Order{
		ID:                p.ID,
		CustomerID:        p.CustomerID,
		RestaurantID:      p.Restaurant.ID,
		RestaurantOwnerID: p.Restaurant.OwnerID,
		RestaurantName:    p.Restaurant.Name,
		RestaurantAddress: p.Restaurant.Address,
		Items:             items,
		ItemsTotal:        itemsTotal,
		DeliveryFee:       fee,
		Total:             itemsTotal + fee,
		Status:            StatusCreated,
		DeliveryAddress:   address,
		Comment:           comment,
		IdempotencyKey:    strings.TrimSpace(p.IdempotencyKey),
		CreatedAt:         p.Now,
		UpdatedAt:         p.Now,
	}
	change := StatusChange{Status: StatusCreated, At: p.Now}
	o.History = []StatusChange{change}
	o.pending = []StatusChange{change}
	return o, nil
}

// TransitionTo переводит заказ в статус next, если переход допустим.
func (o *Order) TransitionTo(next Status, reason string, now time.Time) error {
	if !o.Status.CanTransitionTo(next) {
		return apperr.Wrap(apperr.KindPrecondition,
			"операция недоступна: заказ "+o.Status.Title(), ErrInvalidTransition)
	}
	o.Status = next
	o.UpdatedAt = now
	if next == StatusCancelled {
		o.CancelReason = reason
	}
	change := StatusChange{Status: next, Reason: reason, At: now}
	o.History = append(o.History, change)
	o.pending = append(o.pending, change)
	return nil
}

// AssignCourier сохраняет сведения о назначенном курьере.
func (o *Order) AssignCourier(courierID, courierName string, now time.Time) {
	o.CourierID = courierID
	o.CourierName = courierName
	o.UpdatedAt = now
}

// PendingChanges возвращает несохранённые изменения статуса.
func (o *Order) PendingChanges() []StatusChange { return o.pending }

// ClearPending отмечает изменения сохранёнными.
func (o *Order) ClearPending() { o.pending = nil }

// ItemsCount — общее количество единиц в заказе.
func (o *Order) ItemsCount() int {
	n := 0
	for _, it := range o.Items {
		n += int(it.Quantity)
	}
	return n
}

// ListFilter — параметры выборки заказов.
type ListFilter struct {
	CustomerID        string
	RestaurantID      string
	RestaurantOwnerID string
	Statuses          []Status
	Limit             int
	Offset            int
}

// Stats — агрегированная статистика заказов.
type Stats struct {
	TotalOrders  int64
	ByStatus     map[Status]int64
	Revenue      int64
	AverageCheck int64
	OrdersToday  int64
}
