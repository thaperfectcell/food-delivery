//go:build e2e

// Сквозные (end-to-end) тесты: проверяют систему целиком через REST API шлюза.
// Требуют запущенного окружения (docker compose up) — адрес задаётся E2E_BASE_URL.
//
//	go test -tags e2e -v ./test/e2e/...
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

var baseURL = func() string {
	if v := os.Getenv("E2E_BASE_URL"); v != "" {
		return v
	}
	return "http://localhost:8080"
}()

const (
	pizzeriaID = "10000000-0000-4000-8000-000000000001"
	margherita = "11000000-0000-4000-8000-000000000001"
	lemonade   = "11000000-0000-4000-8000-000000000007"
	password   = "demo12345"
)

type client struct {
	t     *testing.T
	token string
}

func (c *client) do(method, path string, body any, out any, headers ...string) int {
	c.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, baseURL+"/api/v1"+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func (c *client) must(method, path string, body, out any, want int, headers ...string) {
	c.t.Helper()
	var raw json.RawMessage
	code := c.do(method, path, body, &raw, headers...)
	if code != want {
		c.t.Fatalf("%s %s: status %d, want %d: %s", method, path, code, want, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("decode %s: %v", path, err)
		}
	}
}

func login(t *testing.T, email string) *client {
	c := &client{t: t}
	var resp struct {
		Token string `json:"token"`
	}
	c.must("POST", "/auth/login", map[string]string{"email": email, "password": password}, &resp, http.StatusOK)
	c.token = resp.Token
	return c
}

type order struct {
	ID       string                   `json:"id"`
	Status   string                   `json:"status"`
	Total    int64                    `json:"total"`
	Payment  *struct{ Status string } `json:"payment"`
	Delivery *struct {
		ID         string `json:"id"`
		Status     string `json:"status"`
		OrderReady bool   `json:"order_ready"`
	} `json:"delivery"`
}

// waitFor опрашивает заказ, пока условие не выполнится (асинхронная сага).
func waitFor(t *testing.T, c *client, id string, desc string, cond func(order) bool) order {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var o order
	for time.Now().Before(deadline) {
		c.must("GET", "/orders/"+id, nil, &o, http.StatusOK)
		if cond(o) {
			return o
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s, last state: %+v", desc, o)
	return o
}

// waitForOrTimeout — как waitFor, но по истечении времени возвращает последнее состояние.
func waitForOrTimeout(t *testing.T, c *client, id string, d time.Duration, cond func(order) bool) order {
	t.Helper()
	deadline := time.Now().Add(d)
	var o order
	for time.Now().Before(deadline) {
		c.must("GET", "/orders/"+id, nil, &o, http.StatusOK)
		if cond(o) {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	return o
}

func newCustomer(t *testing.T) *client {
	c := &client{t: t}
	var resp struct {
		Token string `json:"token"`
	}
	email := fmt.Sprintf("e2e-%s@test.local", uuid.NewString()[:8])
	c.must("POST", "/auth/register", map[string]string{
		"email": email, "password": "password123", "name": "E2E Клиент", "role": "customer",
	}, &resp, http.StatusCreated)
	c.token = resp.Token
	return c
}

func placeOrder(t *testing.T, c *client, qty int) order {
	var o order
	c.must("POST", "/orders", map[string]any{
		"restaurant_id":    pizzeriaID,
		"items":            []map[string]any{{"menu_item_id": margherita, "quantity": qty}, {"menu_item_id": lemonade, "quantity": 1}},
		"delivery_address": "Москва, ул. Пушкина, 10",
	}, &o, http.StatusCreated, "Idempotency-Key", uuid.NewString())
	return o
}

// newCourier регистрирует курьера и выводит его на смену.
func newCourier(t *testing.T) *client {
	c := &client{t: t}
	var resp struct {
		Token string `json:"token"`
	}
	c.must("POST", "/auth/register", map[string]string{
		"email": fmt.Sprintf("e2e-courier-%s@test.local", uuid.NewString()[:8]), "password": "password123",
		"name": "E2E Курьер", "role": "courier",
	}, &resp, http.StatusCreated)
	c.token = resp.Token
	c.must("POST", "/courier/status", map[string]bool{"online": true}, nil, http.StatusOK)
	return c
}

// assignedCourier находит среди известных курьеров того, кому назначена доставка
// (на общем стенде доставку может получить любой свободный курьер).
func assignedCourier(t *testing.T, deliveryID string, candidates ...*client) *client {
	t.Helper()
	for _, c := range candidates {
		var list struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		if c.do("GET", "/courier/deliveries?active=true", nil, &list) != http.StatusOK {
			continue
		}
		for _, d := range list.Items {
			if d.ID == deliveryID {
				return c
			}
		}
	}
	t.Skip("delivery was assigned to a courier unknown to the test (shared environment)")
	return nil
}

func TestHappyPath(t *testing.T) {
	customer := newCustomer(t)
	restaurant := login(t, "mario@foodflow.local")
	courier := newCourier(t)

	o := placeOrder(t, customer, 2)
	if o.Status != "created" {
		t.Fatalf("new order status = %s", o.Status)
	}
	waitFor(t, customer, o.ID, "paid", func(x order) bool { return x.Status == "paid" })

	restaurant.must("POST", "/orders/"+o.ID+"/accept", nil, nil, http.StatusOK)
	// Назначение идёт в порядке очереди (FIFO): если на стенде остались более ранние
	// ожидающие доставки, выводим на смену дополнительных курьеров.
	couriers := []*client{courier}
	var got order
	for attempt := 0; ; attempt++ {
		got = waitForOrTimeout(t, customer, o.ID, 3*time.Second, func(x order) bool {
			return x.Delivery != nil && x.Delivery.Status == "assigned"
		})
		if got.Delivery != nil && got.Delivery.Status == "assigned" {
			break
		}
		if attempt == 5 {
			t.Fatalf("courier was not assigned: %+v", got)
		}
		couriers = append(couriers, newCourier(t))
	}
	courier = assignedCourier(t, got.Delivery.ID, append(couriers, login(t, "courier@foodflow.local"), login(t, "courier2@foodflow.local"))...)
	// Курьер не может забрать заказ, пока ресторан его не приготовил.
	if code := courier.do("POST", "/courier/deliveries/"+got.Delivery.ID+"/pickup", nil, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("pickup before ready: status %d, want 422", code)
	}
	restaurant.must("POST", "/orders/"+o.ID+"/ready", nil, nil, http.StatusOK)
	waitFor(t, customer, o.ID, "delivery ready", func(x order) bool { return x.Delivery != nil && x.Delivery.OrderReady })

	courier.must("POST", "/courier/deliveries/"+got.Delivery.ID+"/pickup", nil, nil, http.StatusOK)
	waitFor(t, customer, o.ID, "in_delivery", func(x order) bool { return x.Status == "in_delivery" })
	courier.must("POST", "/courier/deliveries/"+got.Delivery.ID+"/deliver", nil, nil, http.StatusOK)
	final := waitFor(t, customer, o.ID, "delivered", func(x order) bool { return x.Status == "delivered" })
	if final.Payment == nil || final.Payment.Status != "succeeded" {
		t.Fatalf("payment must be succeeded: %+v", final.Payment)
	}
	// Курьер снова свободен — уходим со смены, чтобы не влиять на другие тесты.
	courier.do("POST", "/courier/status", map[string]bool{"online": false}, nil)
}

func TestRejectAfterPaymentRefunds(t *testing.T) {
	customer := newCustomer(t)
	restaurant := login(t, "mario@foodflow.local")
	o := placeOrder(t, customer, 1)
	waitFor(t, customer, o.ID, "paid", func(x order) bool { return x.Status == "paid" })
	restaurant.must("POST", "/orders/"+o.ID+"/reject", map[string]string{"reason": "нет ингредиентов"}, nil, http.StatusOK)
	waitFor(t, customer, o.ID, "refund", func(x order) bool {
		return x.Status == "cancelled" && x.Payment != nil && x.Payment.Status == "refunded"
	})
}

func TestPaymentLimitCancelsOrder(t *testing.T) {
	customer := newCustomer(t)
	o := placeOrder(t, customer, 50) // 50 × 549 ₽ > лимита 10 000 ₽
	waitFor(t, customer, o.ID, "cancel on payment failure", func(x order) bool {
		return x.Status == "cancelled" && x.Payment != nil && x.Payment.Status == "failed"
	})
}

func TestSecurity(t *testing.T) {
	anon := &client{t: t}
	if code := anon.do("GET", "/orders", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous /orders: %d, want 401", code)
	}
	courier := login(t, "courier@foodflow.local")
	if code := courier.do("POST", "/orders", map[string]any{"restaurant_id": pizzeriaID}, nil); code != http.StatusForbidden {
		t.Fatalf("courier creating order: %d, want 403", code)
	}
	a := newCustomer(t)
	b := newCustomer(t)
	o := placeOrder(t, a, 1)
	if code := b.do("GET", "/orders/"+o.ID, nil, nil); code != http.StatusForbidden {
		t.Fatalf("foreign order: %d, want 403", code)
	}
}

func TestIdempotentOrderCreation(t *testing.T) {
	c := newCustomer(t)
	key := uuid.NewString()
	body := map[string]any{
		"restaurant_id":    pizzeriaID,
		"items":            []map[string]any{{"menu_item_id": margherita, "quantity": 1}},
		"delivery_address": "Москва, ул. Пушкина, 10",
	}
	var first, second order
	c.must("POST", "/orders", body, &first, http.StatusCreated, "Idempotency-Key", key)
	code := c.do("POST", "/orders", body, &second, "Idempotency-Key", key)
	if (code != http.StatusCreated && code != http.StatusOK) || first.ID != second.ID {
		t.Fatalf("idempotent retry returned %d, ids %s / %s", code, first.ID, second.ID)
	}
}
