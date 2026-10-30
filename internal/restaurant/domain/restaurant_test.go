package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/thaperfectcell/food-delivery/pkg/apperr"
)

var now = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

func TestNewRestaurant(t *testing.T) {
	r, err := NewRestaurant("r1", "owner", RestaurantInput{Name: "  Марио ", Address: " Тверская, 12 ", Cuisine: "Итальянская"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "Марио" || r.Address != "Тверская, 12" || !r.IsActive || r.OwnerID != "owner" {
		t.Fatalf("unexpected restaurant: %+v", r)
	}
	if _, err := NewRestaurant("r2", "o", RestaurantInput{Name: " ", Address: "a"}, now); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("name is required, got %v", err)
	}
	if _, err := NewRestaurant("r3", "o", RestaurantInput{Name: "n"}, now); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("address is required, got %v", err)
	}
	if _, err := NewRestaurant("r4", "o", RestaurantInput{Name: strings.Repeat("я", 101), Address: "a"}, now); err == nil {
		t.Fatal("long name must be rejected")
	}
}

func TestRestaurantApplyIsAtomic(t *testing.T) {
	r, _ := NewRestaurant("r1", "owner", RestaurantInput{Name: "Марио", Address: "Тверская, 12"}, now)
	err := r.Apply(RestaurantPatch{Name: ptr("Новое имя"), Address: ptr("  ")}, now.Add(time.Hour))
	if err == nil {
		t.Fatal("empty address must be rejected")
	}
	if r.Name != "Марио" {
		t.Fatal("failed patch must not change the restaurant")
	}
	if err := r.Apply(RestaurantPatch{IsActive: ptr(false), Cuisine: ptr("Пицца")}, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if r.IsActive || r.Cuisine != "Пицца" || r.Name != "Марио" || !r.UpdatedAt.After(now) {
		t.Fatalf("unexpected restaurant after patch: %+v", r)
	}
}

func TestMenuItemPrice(t *testing.T) {
	for _, p := range []int64{0, -1, MaxPrice + 1} {
		if _, err := NewMenuItem("m", "r", MenuItemInput{Name: "Пицца", Price: p}, now); !apperr.Is(err, apperr.KindInvalid) {
			t.Errorf("price %d must be rejected, got %v", p, err)
		}
	}
	m, err := NewMenuItem("m", "r", MenuItemInput{Name: "Пицца", Category: "Пицца", Price: 549_00}, now)
	if err != nil || !m.IsAvailable {
		t.Fatalf("valid item rejected: %v", err)
	}
	if err := m.Apply(MenuItemPatch{Price: ptr(int64(0))}, now); err == nil {
		t.Fatal("zero price patch must be rejected")
	}
	if err := m.Apply(MenuItemPatch{Price: ptr(int64(599_00)), IsAvailable: ptr(false)}, now); err != nil {
		t.Fatal(err)
	}
	if m.Price != 599_00 || m.IsAvailable {
		t.Fatalf("unexpected item after patch: %+v", m)
	}
}
