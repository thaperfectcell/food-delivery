package httpapi

import (
	"context"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	deliveryv1 "github.com/thaperfectcell/food-delivery/gen/delivery/v1"
	"github.com/thaperfectcell/food-delivery/internal/gateway/httpx"
	"github.com/thaperfectcell/food-delivery/internal/gateway/middleware"
)

// courierMe — GET /courier/me. Курьер, ни разу не выходивший на смену, ещё
// не известен Delivery Service — для него возвращается профиль «не на смене».
func (a *API) courierMe(w http.ResponseWriter, r *http.Request) {
	id, _ := middleware.Identity(r)
	ctx, cancel := a.call(r)
	defer cancel()
	c, err := a.c.Delivery.GetCourier(ctx, &deliveryv1.GetCourierRequest{})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			httpx.WriteJSON(w, http.StatusOK, courierDTO{ID: id.UserID, Name: id.Name, Status: "offline"})
			return
		}
		a.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toCourier(c))
}

type courierStatusRequest struct {
	Online *bool `json:"online"`
}

// courierStatus — POST /courier/status: выход на смену или уход со смены.
// Отображаемое имя курьера берётся из JWT (claim name).
func (a *API) courierStatus(w http.ResponseWriter, r *http.Request) {
	var in courierStatusRequest
	if !decodeJSON(w, r, &in, false) {
		return
	}
	if in.Online == nil {
		badRequest(w, r, "укажите поле online (true или false)")
		return
	}
	id, _ := middleware.Identity(r)
	ctx, cancel := a.call(r)
	defer cancel()
	c, err := a.c.Delivery.SetCourierStatus(ctx, &deliveryv1.SetCourierStatusRequest{Online: *in.Online, Name: id.Name})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toCourier(c))
}

// courierDeliveries — GET /courier/deliveries?active=true.
func (a *API) courierDeliveries(w http.ResponseWriter, r *http.Request) {
	limit, _, ok := paging(w, r)
	if !ok {
		return
	}
	if limit == 0 {
		limit = 50
	}
	ctx, cancel := a.call(r)
	defer cancel()
	resp, err := a.c.Delivery.ListCourierDeliveries(ctx, &deliveryv1.ListCourierDeliveriesRequest{
		ActiveOnly: queryBool(r, "active"),
		Limit:      limit,
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	items := make([]deliveryDTO, 0, len(resp.GetDeliveries()))
	for _, d := range resp.GetDeliveries() {
		items = append(items, *toDelivery(d))
	}
	httpx.WriteJSON(w, http.StatusOK, newList(items, len(items)))
}

// pickUpDelivery — POST /courier/deliveries/{id}/pickup.
func (a *API) pickUpDelivery(w http.ResponseWriter, r *http.Request) {
	a.deliveryAction(w, r, a.c.Delivery.PickUpDelivery)
}

// completeDelivery — POST /courier/deliveries/{id}/deliver.
func (a *API) completeDelivery(w http.ResponseWriter, r *http.Request) {
	a.deliveryAction(w, r, a.c.Delivery.CompleteDelivery)
}

type deliveryCall func(ctx context.Context, req *deliveryv1.DeliveryActionRequest, opts ...grpc.CallOption) (*deliveryv1.Delivery, error)

func (a *API) deliveryAction(w http.ResponseWriter, r *http.Request, call deliveryCall) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	ctx, cancel := a.call(r)
	defer cancel()
	d, err := call(ctx, &deliveryv1.DeliveryActionRequest{Id: id})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toDelivery(d))
}
