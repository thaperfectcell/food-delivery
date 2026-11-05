package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	deliveryv1 "github.com/thaperfectcell/food-delivery/gen/delivery/v1"
	orderv1 "github.com/thaperfectcell/food-delivery/gen/order/v1"
	paymentv1 "github.com/thaperfectcell/food-delivery/gen/payment/v1"
	"github.com/thaperfectcell/food-delivery/internal/gateway/httpx"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
)

// HeaderIdempotencyKey — заголовок ключа идемпотентности создания заказа.
const HeaderIdempotencyKey = "Idempotency-Key"

type createOrderRequest struct {
	RestaurantID string `json:"restaurant_id"`
	Items        []struct {
		MenuItemID string `json:"menu_item_id"`
		Quantity   int32  `json:"quantity"`
	} `json:"items"`
	DeliveryAddress string `json:"delivery_address"`
	Comment         string `json:"comment"`
}

// createOrder — POST /orders. Ключ идемпотентности из заголовка передаётся в
// Order Service: повтор запроса (например, после обрыва связи) вернёт уже
// созданный заказ, а не создаст второй.
func (a *API) createOrder(w http.ResponseWriter, r *http.Request) {
	var in createOrderRequest
	if !decodeJSON(w, r, &in, false) {
		return
	}
	key := strings.TrimSpace(r.Header.Get(HeaderIdempotencyKey))
	if len(key) > 100 {
		badRequest(w, r, "ключ идемпотентности слишком длинный (максимум 100 символов)")
		return
	}
	req := &orderv1.CreateOrderRequest{
		RestaurantId:    strings.TrimSpace(in.RestaurantID),
		DeliveryAddress: in.DeliveryAddress,
		Comment:         in.Comment,
		IdempotencyKey:  key,
	}
	for _, it := range in.Items {
		req.Items = append(req.Items, &orderv1.CreateOrderItem{
			MenuItemId: strings.TrimSpace(it.MenuItemID),
			Quantity:   it.Quantity,
		})
	}
	ctx, cancel := a.call(r)
	defer cancel()
	o, err := a.c.Order.CreateOrder(ctx, req)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toOrder(o))
}

// listOrders — GET /orders?status=paid,accepted&restaurant_id=…
func (a *API) listOrders(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := paging(w, r)
	if !ok {
		return
	}
	req := &orderv1.ListOrdersRequest{Limit: limit, Offset: offset}
	if rid := strings.TrimSpace(r.URL.Query().Get("restaurant_id")); rid != "" {
		req.RestaurantId = rid
	}
	for _, raw := range strings.Split(r.URL.Query().Get("status"), ",") {
		if raw = strings.TrimSpace(raw); raw == "" {
			continue
		}
		st, ok := orderStatusFromName(raw)
		if !ok {
			badRequest(w, r, "неизвестный статус заказа: "+raw)
			return
		}
		req.Statuses = append(req.Statuses, st)
	}
	ctx, cancel := a.call(r)
	defer cancel()
	resp, err := a.c.Order.ListOrders(ctx, req)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	items := make([]orderDTO, 0, len(resp.GetOrders()))
	for _, o := range resp.GetOrders() {
		items = append(items, toOrder(o))
	}
	httpx.WriteJSON(w, http.StatusOK, newList(items, int(resp.GetTotal())))
}

// getOrder — GET /orders/{id}: паттерн API Composition.
//
//  1. Заказ запрашивается у Order Service от имени пользователя — сервис
//     проверяет право доступа к заказу.
//  2. После успешной проверки данные об оплате (Payment Service) и доставке
//     (Delivery Service) запрашиваются параллельно от имени системы:
//     шлюз уже убедился, что пользователь — участник заказа.
//  3. Недоступность Payment/Delivery не ломает ответ (graceful degradation):
//     соответствующая часть возвращается как null.
func (a *API) getOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	ctx, cancel := a.call(r)
	defer cancel()
	o, err := a.c.Order.GetOrder(ctx, &orderv1.GetOrderRequest{Id: id})
	if err != nil {
		a.writeError(w, r, err)
		return
	}

	details := orderDetailsDTO{orderDTO: toOrder(o)}
	sysCtx := auth.WithIdentity(ctx, auth.System)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		p, err := a.c.Payment.GetPaymentByOrder(sysCtx, &paymentv1.GetPaymentByOrderRequest{OrderId: id})
		if err != nil {
			a.logPartial(ctx, "payment", err)
			return
		}
		details.Payment = toPayment(p)
	}()
	go func() {
		defer wg.Done()
		d, err := a.c.Delivery.GetDeliveryByOrder(sysCtx, &deliveryv1.GetDeliveryByOrderRequest{OrderId: id})
		if err != nil {
			a.logPartial(ctx, "delivery", err)
			return
		}
		details.Delivery = toDelivery(d)
	}()
	wg.Wait()
	httpx.WriteJSON(w, http.StatusOK, details)
}

// logPartial журналирует сбой необязательной части составного ответа
// (NotFound — штатная ситуация: оплаты или доставки ещё нет).
func (a *API) logPartial(ctx context.Context, part string, err error) {
	if status.Code(err) == codes.NotFound {
		return
	}
	a.log.WarnContext(ctx, "composition part unavailable",
		slog.String("part", part), slog.String("error", status.Convert(err).Message()))
}

type reasonRequest struct {
	Reason string `json:"reason"`
}

// cancelOrder — POST /orders/{id}/cancel (клиент).
func (a *API) cancelOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in reasonRequest
	if !decodeJSON(w, r, &in, true) {
		return
	}
	a.orderAction(w, r, func(ctx context.Context) (*orderv1.Order, error) {
		return a.c.Order.CancelOrder(ctx, &orderv1.CancelOrderRequest{Id: id, Reason: strings.TrimSpace(in.Reason)})
	})
}

// acceptOrder — POST /orders/{id}/accept (ресторан).
func (a *API) acceptOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	a.orderAction(w, r, func(ctx context.Context) (*orderv1.Order, error) {
		return a.c.Order.AcceptOrder(ctx, &orderv1.AcceptOrderRequest{Id: id})
	})
}

// rejectOrder — POST /orders/{id}/reject (ресторан).
func (a *API) rejectOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in reasonRequest
	if !decodeJSON(w, r, &in, true) {
		return
	}
	a.orderAction(w, r, func(ctx context.Context) (*orderv1.Order, error) {
		return a.c.Order.RejectOrder(ctx, &orderv1.RejectOrderRequest{Id: id, Reason: strings.TrimSpace(in.Reason)})
	})
}

// markOrderReady — POST /orders/{id}/ready (ресторан).
func (a *API) markOrderReady(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	a.orderAction(w, r, func(ctx context.Context) (*orderv1.Order, error) {
		return a.c.Order.MarkOrderReady(ctx, &orderv1.MarkOrderReadyRequest{Id: id})
	})
}

func (a *API) orderAction(w http.ResponseWriter, r *http.Request, fn func(ctx context.Context) (*orderv1.Order, error)) {
	ctx, cancel := a.call(r)
	defer cancel()
	o, err := fn(ctx)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toOrder(o))
}
