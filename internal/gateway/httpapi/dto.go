package httpapi

import (
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/thaperfectcell/food-delivery/gen/auth/v1"
	deliveryv1 "github.com/thaperfectcell/food-delivery/gen/delivery/v1"
	notificationv1 "github.com/thaperfectcell/food-delivery/gen/notification/v1"
	orderv1 "github.com/thaperfectcell/food-delivery/gen/order/v1"
	paymentv1 "github.com/thaperfectcell/food-delivery/gen/payment/v1"
	restaurantv1 "github.com/thaperfectcell/food-delivery/gen/restaurant/v1"
	"github.com/thaperfectcell/food-delivery/pkg/events"
)

// Объекты передачи данных (DTO) внешнего REST API. Их структура зафиксирована
// в api/openapi.yaml и не зависит от внутренних protobuf-контрактов: шлюз
// преобразует сообщения сервисов в стабильный публичный формат (снейк-кейс,
// статусы строками в нижнем регистре, деньги — в копейках).

// List — единый формат ответа со списком.
type List[T any] struct {
	Items []T `json:"items"`
	Total int `json:"total"`
}

func newList[T any](items []T, total int) List[T] {
	if items == nil {
		items = []T{}
	}
	return List[T]{Items: items, Total: total}
}

// ---------------------------------------------------------------- auth

type userDTO struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Phone     string    `json:"phone"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type authResponseDTO struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	User      userDTO   `json:"user"`
}

func toUser(u *authv1.User) userDTO {
	return userDTO{
		ID:        u.GetId(),
		Email:     u.GetEmail(),
		Name:      u.GetName(),
		Phone:     u.GetPhone(),
		Role:      roleName(u.GetRole()),
		CreatedAt: ts(u.GetCreatedAt()),
	}
}

func toAuthResponse(r *authv1.AuthResponse) authResponseDTO {
	return authResponseDTO{
		Token:     r.GetAccessToken(),
		ExpiresAt: ts(r.GetExpiresAt()),
		User:      toUser(r.GetUser()),
	}
}

func roleName(r authv1.Role) string {
	return enumName(r.String(), "ROLE_")
}

// roleFromName — роль из строки API ("" → ROLE_UNSPECIFIED).
func roleFromName(s string) (authv1.Role, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return authv1.Role_ROLE_UNSPECIFIED, true
	case "customer":
		return authv1.Role_ROLE_CUSTOMER, true
	case "restaurant":
		return authv1.Role_ROLE_RESTAURANT, true
	case "courier":
		return authv1.Role_ROLE_COURIER, true
	case "admin":
		return authv1.Role_ROLE_ADMIN, true
	}
	return authv1.Role_ROLE_UNSPECIFIED, false
}

// ---------------------------------------------------------------- catalog

type restaurantDTO struct {
	ID          string    `json:"id"`
	OwnerID     string    `json:"owner_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Address     string    `json:"address"`
	Cuisine     string    `json:"cuisine"`
	IsActive    bool      `json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
}

func toRestaurant(r *restaurantv1.Restaurant) restaurantDTO {
	return restaurantDTO{
		ID:          r.GetId(),
		OwnerID:     r.GetOwnerId(),
		Name:        r.GetName(),
		Description: r.GetDescription(),
		Address:     r.GetAddress(),
		Cuisine:     r.GetCuisine(),
		IsActive:    r.GetIsActive(),
		CreatedAt:   ts(r.GetCreatedAt()),
	}
}

type menuItemDTO struct {
	ID           string    `json:"id"`
	RestaurantID string    `json:"restaurant_id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Category     string    `json:"category"`
	Price        int64     `json:"price"`
	IsAvailable  bool      `json:"is_available"`
	CreatedAt    time.Time `json:"created_at"`
}

func toMenuItem(m *restaurantv1.MenuItem) menuItemDTO {
	return menuItemDTO{
		ID:           m.GetId(),
		RestaurantID: m.GetRestaurantId(),
		Name:         m.GetName(),
		Description:  m.GetDescription(),
		Category:     m.GetCategory(),
		Price:        m.GetPrice(),
		IsAvailable:  m.GetIsAvailable(),
		CreatedAt:    ts(m.GetCreatedAt()),
	}
}

// ---------------------------------------------------------------- orders

type orderItemDTO struct {
	MenuItemID string `json:"menu_item_id"`
	Name       string `json:"name"`
	Price      int64  `json:"price"`
	Quantity   int32  `json:"quantity"`
	Total      int64  `json:"total"`
}

type statusChangeDTO struct {
	Status string    `json:"status"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

type orderDTO struct {
	ID                string            `json:"id"`
	Number            string            `json:"number"`
	CustomerID        string            `json:"customer_id"`
	RestaurantID      string            `json:"restaurant_id"`
	RestaurantName    string            `json:"restaurant_name"`
	RestaurantAddress string            `json:"restaurant_address"`
	Items             []orderItemDTO    `json:"items"`
	ItemsTotal        int64             `json:"items_total"`
	DeliveryFee       int64             `json:"delivery_fee"`
	Total             int64             `json:"total"`
	Status            string            `json:"status"`
	DeliveryAddress   string            `json:"delivery_address"`
	Comment           string            `json:"comment"`
	CourierID         string            `json:"courier_id"`
	CourierName       string            `json:"courier_name"`
	CancelReason      string            `json:"cancel_reason"`
	History           []statusChangeDTO `json:"history"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
}

// orderDetailsDTO — заказ, дополненный данными Payment и Delivery Service
// (паттерн API Composition). Отсутствующие части сериализуются как null.
type orderDetailsDTO struct {
	orderDTO
	Payment  *paymentDTO  `json:"payment"`
	Delivery *deliveryDTO `json:"delivery"`
}

func toOrder(o *orderv1.Order) orderDTO {
	d := orderDTO{
		ID:                o.GetId(),
		Number:            events.ShortID(o.GetId()),
		CustomerID:        o.GetCustomerId(),
		RestaurantID:      o.GetRestaurantId(),
		RestaurantName:    o.GetRestaurantName(),
		RestaurantAddress: o.GetRestaurantAddress(),
		Items:             make([]orderItemDTO, 0, len(o.GetItems())),
		ItemsTotal:        o.GetItemsTotal(),
		DeliveryFee:       o.GetDeliveryFee(),
		Total:             o.GetTotal(),
		Status:            orderStatusName(o.GetStatus()),
		DeliveryAddress:   o.GetDeliveryAddress(),
		Comment:           o.GetComment(),
		CourierID:         o.GetCourierId(),
		CourierName:       o.GetCourierName(),
		CancelReason:      o.GetCancelReason(),
		History:           make([]statusChangeDTO, 0, len(o.GetHistory())),
		CreatedAt:         ts(o.GetCreatedAt()),
		UpdatedAt:         ts(o.GetUpdatedAt()),
	}
	for _, it := range o.GetItems() {
		d.Items = append(d.Items, orderItemDTO{
			MenuItemID: it.GetMenuItemId(),
			Name:       it.GetName(),
			Price:      it.GetPrice(),
			Quantity:   it.GetQuantity(),
			Total:      it.GetTotal(),
		})
	}
	for _, h := range o.GetHistory() {
		d.History = append(d.History, statusChangeDTO{
			Status: orderStatusName(h.GetStatus()),
			Reason: h.GetReason(),
			At:     ts(h.GetAt()),
		})
	}
	return d
}

func orderStatusName(s orderv1.OrderStatus) string {
	return enumName(s.String(), "ORDER_STATUS_")
}

// orderStatusFromName разбирает статус заказа из строки API.
func orderStatusFromName(s string) (orderv1.OrderStatus, bool) {
	name := "ORDER_STATUS_" + strings.ToUpper(strings.TrimSpace(s))
	v, ok := orderv1.OrderStatus_value[name]
	if !ok || v == int32(orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED) {
		return orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED, false
	}
	return orderv1.OrderStatus(v), true
}

// ---------------------------------------------------------------- payments

type paymentDTO struct {
	ID            string    `json:"id"`
	OrderID       string    `json:"order_id"`
	Amount        int64     `json:"amount"`
	Status        string    `json:"status"`
	FailureReason string    `json:"failure_reason"`
	ProviderRef   string    `json:"provider_ref"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func toPayment(p *paymentv1.Payment) *paymentDTO {
	if p == nil {
		return nil
	}
	return &paymentDTO{
		ID:            p.GetId(),
		OrderID:       p.GetOrderId(),
		Amount:        p.GetAmount(),
		Status:        enumName(p.GetStatus().String(), "PAYMENT_STATUS_"),
		FailureReason: p.GetFailureReason(),
		ProviderRef:   p.GetProviderRef(),
		CreatedAt:     ts(p.GetCreatedAt()),
		UpdatedAt:     ts(p.GetUpdatedAt()),
	}
}

// ---------------------------------------------------------------- delivery

type courierDTO struct {
	ID                  string     `json:"id"`
	Name                string     `json:"name"`
	Status              string     `json:"status"`
	CompletedDeliveries int32      `json:"completed_deliveries"`
	StatusChangedAt     *time.Time `json:"status_changed_at"`
}

func toCourier(c *deliveryv1.Courier) courierDTO {
	return courierDTO{
		ID:                  c.GetId(),
		Name:                c.GetName(),
		Status:              enumName(c.GetStatus().String(), "COURIER_STATUS_"),
		CompletedDeliveries: c.GetCompletedDeliveries(),
		StatusChangedAt:     optTS(c.GetStatusChangedAt()),
	}
}

type deliveryDTO struct {
	ID                string     `json:"id"`
	OrderID           string     `json:"order_id"`
	OrderNumber       string     `json:"order_number"`
	CourierID         string     `json:"courier_id"`
	CourierName       string     `json:"courier_name"`
	Status            string     `json:"status"`
	RestaurantName    string     `json:"restaurant_name"`
	RestaurantAddress string     `json:"restaurant_address"`
	DeliveryAddress   string     `json:"delivery_address"`
	OrderTotal        int64      `json:"order_total"`
	OrderReady        bool       `json:"order_ready"`
	CreatedAt         time.Time  `json:"created_at"`
	AssignedAt        *time.Time `json:"assigned_at"`
	PickedUpAt        *time.Time `json:"picked_up_at"`
	DeliveredAt       *time.Time `json:"delivered_at"`
}

func toDelivery(d *deliveryv1.Delivery) *deliveryDTO {
	if d == nil {
		return nil
	}
	return &deliveryDTO{
		ID:                d.GetId(),
		OrderID:           d.GetOrderId(),
		OrderNumber:       events.ShortID(d.GetOrderId()),
		CourierID:         d.GetCourierId(),
		CourierName:       d.GetCourierName(),
		Status:            enumName(d.GetStatus().String(), "DELIVERY_STATUS_"),
		RestaurantName:    d.GetRestaurantName(),
		RestaurantAddress: d.GetRestaurantAddress(),
		DeliveryAddress:   d.GetDeliveryAddress(),
		OrderTotal:        d.GetOrderTotal(),
		OrderReady:        d.GetOrderReady(),
		CreatedAt:         ts(d.GetCreatedAt()),
		AssignedAt:        optTS(d.GetAssignedAt()),
		PickedUpAt:        optTS(d.GetPickedUpAt()),
		DeliveredAt:       optTS(d.GetDeliveredAt()),
	}
}

// ---------------------------------------------------------------- notifications

type notificationDTO struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	OrderID   string    `json:"order_id"`
	Read      bool      `json:"read"`
	CreatedAt time.Time `json:"created_at"`
}

type notificationListDTO struct {
	Items  []notificationDTO `json:"items"`
	Total  int               `json:"total"`
	Unread int32             `json:"unread"`
}

func toNotification(n *notificationv1.Notification) notificationDTO {
	return notificationDTO{
		ID:        n.GetId(),
		Type:      n.GetType(),
		Title:     n.GetTitle(),
		Body:      n.GetBody(),
		OrderID:   n.GetOrderId(),
		Read:      n.GetRead(),
		CreatedAt: ts(n.GetCreatedAt()),
	}
}

// ---------------------------------------------------------------- admin

type orderStatsDTO struct {
	Total        int64            `json:"total"`
	Today        int64            `json:"today"`
	Revenue      int64            `json:"revenue"`
	AverageCheck int64            `json:"average_check"`
	ByStatus     map[string]int64 `json:"by_status"`
}

type deliveryStatsDTO struct {
	CouriersOnline      int32 `json:"couriers_online"`
	CouriersBusy        int32 `json:"couriers_busy"`
	DeliveriesPending   int32 `json:"deliveries_pending"`
	DeliveriesActive    int32 `json:"deliveries_active"`
	DeliveriesCompleted int32 `json:"deliveries_completed"`
}

type statsDTO struct {
	Orders      orderStatsDTO    `json:"orders"`
	Delivery    deliveryStatsDTO `json:"delivery"`
	Restaurants int              `json:"restaurants"`
	Users       int              `json:"users"`
	// Warnings перечисляет части сводки, которые не удалось получить
	// (graceful degradation: недоступность одного сервиса не ломает ответ).
	Warnings []string `json:"warnings,omitempty"`
}

func toOrderStats(s *orderv1.OrderStats) orderStatsDTO {
	by := make(map[string]int64, len(s.GetByStatus()))
	for k, v := range s.GetByStatus() {
		by[k] = v
	}
	return orderStatsDTO{
		Total:        s.GetTotalOrders(),
		Today:        s.GetOrdersToday(),
		Revenue:      s.GetRevenue(),
		AverageCheck: s.GetAverageCheck(),
		ByStatus:     by,
	}
}

func toDeliveryStats(s *deliveryv1.DeliveryStats) deliveryStatsDTO {
	return deliveryStatsDTO{
		CouriersOnline:      s.GetCouriersOnline(),
		CouriersBusy:        s.GetCouriersBusy(),
		DeliveriesPending:   s.GetDeliveriesPending(),
		DeliveriesActive:    s.GetDeliveriesActive(),
		DeliveriesCompleted: s.GetDeliveriesCompleted(),
	}
}

// ---------------------------------------------------------------- helpers

// enumName превращает имя значения enum protobuf в строку API:
// ORDER_STATUS_IN_DELIVERY → in_delivery. UNSPECIFIED → "".
func enumName(full, prefix string) string {
	s := strings.ToLower(strings.TrimPrefix(full, prefix))
	if s == "unspecified" {
		return ""
	}
	return s
}

// ts преобразует отметку времени protobuf в time.Time (UTC).
func ts(t *timestamppb.Timestamp) time.Time {
	if !isSet(t) {
		return time.Time{}
	}
	return t.AsTime().UTC()
}

// optTS — то же для необязательных полей: незаданное время → null в JSON.
func optTS(t *timestamppb.Timestamp) *time.Time {
	if !isSet(t) {
		return nil
	}
	v := t.AsTime().UTC()
	return &v
}

func isSet(t *timestamppb.Timestamp) bool {
	if t == nil || (t.GetSeconds() == 0 && t.GetNanos() == 0) {
		return false
	}
	return !t.AsTime().IsZero()
}
