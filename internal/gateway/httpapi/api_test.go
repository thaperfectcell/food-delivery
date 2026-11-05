package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	deliveryv1 "github.com/thaperfectcell/food-delivery/gen/delivery/v1"
	orderv1 "github.com/thaperfectcell/food-delivery/gen/order/v1"
	paymentv1 "github.com/thaperfectcell/food-delivery/gen/payment/v1"
	"github.com/thaperfectcell/food-delivery/internal/gateway/clients"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
	"github.com/thaperfectcell/food-delivery/pkg/logger"
)

const orderID = "6b7b3a60-7e78-4c76-8d99-898c03f58a4b"

// Подделки gRPC-клиентов: встраивание интерфейса позволяет реализовать
// только методы, нужные тесту.
type fakeOrder struct {
	orderv1.OrderServiceClient
	getErr  error
	created *orderv1.CreateOrderRequest
	actor   auth.Identity
}

func (f *fakeOrder) GetOrder(ctx context.Context, in *orderv1.GetOrderRequest, _ ...grpc.CallOption) (*orderv1.Order, error) {
	f.actor, _ = auth.IdentityFrom(ctx)
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &orderv1.Order{Id: in.GetId(), Status: orderv1.OrderStatus_ORDER_STATUS_PAID, Total: 1617_00}, nil
}

func (f *fakeOrder) CreateOrder(ctx context.Context, in *orderv1.CreateOrderRequest, _ ...grpc.CallOption) (*orderv1.Order, error) {
	f.created = in
	return &orderv1.Order{Id: orderID, Status: orderv1.OrderStatus_ORDER_STATUS_CREATED}, nil
}

type fakePayment struct {
	paymentv1.PaymentServiceClient
	actor auth.Identity
}

func (f *fakePayment) GetPaymentByOrder(ctx context.Context, _ *paymentv1.GetPaymentByOrderRequest, _ ...grpc.CallOption) (*paymentv1.Payment, error) {
	f.actor, _ = auth.IdentityFrom(ctx)
	return nil, status.Error(codes.NotFound, "платёж не найден")
}

type fakeDelivery struct {
	deliveryv1.DeliveryServiceClient
}

func (fakeDelivery) GetDeliveryByOrder(context.Context, *deliveryv1.GetDeliveryByOrderRequest, ...grpc.CallOption) (*deliveryv1.Delivery, error) {
	return nil, status.Error(codes.Unavailable, "сервис недоступен")
}

type env struct {
	h      http.Handler
	tokens *auth.TokenManager
	order  *fakeOrder
	pay    *fakePayment
}

func newEnv() *env {
	tm := auth.NewTokenManager("test-secret", time.Hour)
	o, p := &fakeOrder{}, &fakePayment{}
	h := NewHandler(Deps{
		Clients: clients.Set{Order: o, Payment: p, Delivery: fakeDelivery{}},
		Tokens:  tm,
		Log:     logger.Discard(),
	})
	return &env{h: h, tokens: tm, order: o, pay: p}
}

func (e *env) do(t *testing.T, method, path string, role auth.Role, body any, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if role != "" {
		tok, _, err := e.tokens.Issue("00000000-0000-4000-8000-000000000002", role, "Тест")
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func TestOrderDetailsCompositionDegradesGracefully(t *testing.T) {
	e := newEnv()
	rec := e.do(t, "GET", "/api/v1/orders/"+orderID, auth.RoleCustomer, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["status"] != "paid" || got["payment"] != nil || got["delivery"] != nil {
		t.Fatalf("unexpected body: %s", rec.Body)
	}
	// Доступ проверяется от имени пользователя, вспомогательные запросы — от имени системы.
	if e.order.actor.Role != auth.RoleCustomer || e.pay.actor.Role != auth.RoleSystem {
		t.Fatalf("identities: order=%v payment=%v", e.order.actor.Role, e.pay.actor.Role)
	}
}

func TestDownstreamErrorIsMappedToHTTP(t *testing.T) {
	e := newEnv()
	e.order.getErr = status.Error(codes.PermissionDenied, "нет доступа к заказу")
	rec := e.do(t, "GET", "/api/v1/orders/"+orderID, auth.RoleCustomer, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d", rec.Code)
	}
	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error.Message != "нет доступа к заказу" {
		t.Fatalf("error body: %s", rec.Body)
	}
}

func TestCreateOrderRequiresCustomerAndPassesIdempotencyKey(t *testing.T) {
	e := newEnv()
	req := map[string]any{
		"restaurant_id":    "10000000-0000-4000-8000-000000000001",
		"items":            []map[string]any{{"menu_item_id": "11000000-0000-4000-8000-000000000001", "quantity": 2}},
		"delivery_address": "Москва",
	}
	if rec := e.do(t, "POST", "/api/v1/orders", "", req); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rec.Code)
	}
	if rec := e.do(t, "POST", "/api/v1/orders", auth.RoleCourier, req); rec.Code != http.StatusForbidden {
		t.Fatalf("courier: %d", rec.Code)
	}
	rec := e.do(t, "POST", "/api/v1/orders", auth.RoleCustomer, req, "Idempotency-Key", "key-42")
	if rec.Code != http.StatusCreated {
		t.Fatalf("customer: %d %s", rec.Code, rec.Body)
	}
	if e.order.created.GetIdempotencyKey() != "key-42" || e.order.created.GetItems()[0].GetQuantity() != 2 {
		t.Fatalf("request not passed through: %+v", e.order.created)
	}
}

func TestHTTPStatusMapping(t *testing.T) {
	cases := map[codes.Code]int{
		codes.InvalidArgument:    400,
		codes.Unauthenticated:    401,
		codes.PermissionDenied:   403,
		codes.NotFound:           404,
		codes.AlreadyExists:      409,
		codes.FailedPrecondition: 422,
		codes.ResourceExhausted:  429,
		codes.Unavailable:        503,
		codes.DeadlineExceeded:   504,
		codes.Internal:           500,
	}
	for c, want := range cases {
		if got, _ := HTTPStatus(c); got != want {
			t.Errorf("%v: got %d, want %d", c, got, want)
		}
	}
}
