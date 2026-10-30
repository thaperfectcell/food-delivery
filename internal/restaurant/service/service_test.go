package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/thaperfectcell/food-delivery/internal/restaurant/domain"
	"github.com/thaperfectcell/food-delivery/pkg/apperr"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
	"github.com/thaperfectcell/food-delivery/pkg/demo"
	"github.com/thaperfectcell/food-delivery/pkg/logger"
)

// ------------------------------------------------------------ тестовые двойники

type memRepo struct {
	mu          sync.Mutex
	restaurants map[string]*domain.Restaurant
	items       map[string]*domain.MenuItem
}

func newMemRepo() *memRepo {
	return &memRepo{restaurants: map[string]*domain.Restaurant{}, items: map[string]*domain.MenuItem{}}
}

func (m *memRepo) CreateRestaurant(_ context.Context, r *domain.Restaurant) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := *r
	m.restaurants[r.ID] = &c
	return nil
}

func (m *memRepo) GetRestaurant(_ context.Context, id string) (*domain.Restaurant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.restaurants[id]
	if !ok {
		return nil, domain.ErrRestaurantNotFound
	}
	c := *r
	return &c, nil
}

func (m *memRepo) UpdateRestaurant(_ context.Context, r *domain.Restaurant) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.restaurants[r.ID]; !ok {
		return domain.ErrRestaurantNotFound
	}
	c := *r
	m.restaurants[r.ID] = &c
	return nil
}

func (m *memRepo) ListRestaurants(_ context.Context, f domain.ListFilter) ([]*domain.Restaurant, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*domain.Restaurant
	for _, r := range m.restaurants {
		if !f.IncludeInactive && !r.IsActive {
			continue
		}
		if f.OwnerID != "" && r.OwnerID != f.OwnerID {
			continue
		}
		if f.Cuisine != "" && !strings.EqualFold(r.Cuisine, f.Cuisine) {
			continue
		}
		if f.Query != "" && !strings.Contains(strings.ToLower(r.Name), strings.ToLower(f.Query)) {
			continue
		}
		c := *r
		out = append(out, &c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	total := len(out)
	if f.Offset >= len(out) {
		return nil, total, nil
	}
	out = out[f.Offset:]
	if len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, total, nil
}

func (m *memRepo) ListMenu(_ context.Context, restaurantID string, onlyAvailable bool) ([]*domain.MenuItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*domain.MenuItem
	for _, it := range m.items {
		if it.RestaurantID != restaurantID || (onlyAvailable && !it.IsAvailable) {
			continue
		}
		c := *it
		out = append(out, &c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (m *memRepo) GetMenuItem(_ context.Context, restaurantID, id string) (*domain.MenuItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.items[id]
	if !ok || it.RestaurantID != restaurantID {
		return nil, domain.ErrMenuItemNotFound
	}
	c := *it
	return &c, nil
}

func (m *memRepo) GetMenuItemsByIDs(_ context.Context, restaurantID string, ids []string) ([]*domain.MenuItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*domain.MenuItem
	for _, id := range ids {
		if it, ok := m.items[id]; ok && it.RestaurantID == restaurantID {
			c := *it
			out = append(out, &c)
		}
	}
	return out, nil
}

func (m *memRepo) CreateMenuItem(_ context.Context, it *domain.MenuItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := *it
	m.items[it.ID] = &c
	return nil
}

func (m *memRepo) UpdateMenuItem(_ context.Context, it *domain.MenuItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[it.ID]; !ok {
		return domain.ErrMenuItemNotFound
	}
	c := *it
	m.items[it.ID] = &c
	return nil
}

func (m *memRepo) DeleteMenuItem(_ context.Context, restaurantID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.items[id]
	if !ok || it.RestaurantID != restaurantID {
		return domain.ErrMenuItemNotFound
	}
	delete(m.items, id)
	return nil
}

func (m *memRepo) SeedRestaurant(_ context.Context, r *domain.Restaurant, items []*domain.MenuItem) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	added := false
	if _, ok := m.restaurants[r.ID]; !ok {
		c := *r
		m.restaurants[r.ID] = &c
		added = true
	}
	for _, it := range items {
		if _, ok := m.items[it.ID]; !ok {
			c := *it
			m.items[it.ID] = &c
		}
	}
	return added, nil
}

// ------------------------------------------------------------ окружение теста

func ptr[T any](v T) *T { return &v }

var (
	anonymous = auth.Identity{}
	admin     = auth.Identity{UserID: uuid.NewString(), Role: auth.RoleAdmin}
)

func owner() auth.Identity { return auth.Identity{UserID: uuid.NewString(), Role: auth.RoleRestaurant} }

func newService() (*Service, *memRepo) {
	repo := newMemRepo()
	return New(repo, logger.Discard()), repo
}

func mustRestaurant(t *testing.T, svc *Service, actor auth.Identity, name string) *domain.Restaurant {
	t.Helper()
	r, err := svc.CreateRestaurant(context.Background(), actor, domain.RestaurantInput{
		Name: name, Address: "Москва, ул. Тверская, 1", Cuisine: "Итальянская",
	})
	if err != nil {
		t.Fatalf("create restaurant: %v", err)
	}
	return r
}

func mustItem(t *testing.T, svc *Service, actor auth.Identity, restaurantID, name string, price int64) *domain.MenuItem {
	t.Helper()
	m, err := svc.CreateMenuItem(context.Background(), actor, restaurantID, domain.MenuItemInput{
		Name: name, Category: "Пицца", Price: price,
	})
	if err != nil {
		t.Fatalf("create menu item: %v", err)
	}
	return m
}

// ------------------------------------------------------------ тесты

func TestCreateRestaurantRoles(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService()
	o := owner()
	r := mustRestaurant(t, svc, o, "Марио")
	if r.OwnerID != o.UserID || !r.IsActive {
		t.Fatalf("owner must be the caller and restaurant active: %+v", r)
	}
	customer := auth.Identity{UserID: uuid.NewString(), Role: auth.RoleCustomer}
	if _, err := svc.CreateRestaurant(ctx, customer, domain.RestaurantInput{Name: "X", Address: "Y"}); !apperr.Is(err, apperr.KindForbidden) {
		t.Fatalf("customer must not create restaurants, got %v", err)
	}
	if _, err := svc.CreateRestaurant(ctx, anonymous, domain.RestaurantInput{Name: "X", Address: "Y"}); !apperr.Is(err, apperr.KindForbidden) {
		t.Fatalf("anonymous must not create restaurants, got %v", err)
	}
	if _, err := svc.CreateRestaurant(ctx, o, domain.RestaurantInput{Name: "X"}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("address is required, got %v", err)
	}
	if _, err := svc.CreateRestaurant(ctx, admin, domain.RestaurantInput{Name: "Админский", Address: "Y"}); err != nil {
		t.Fatalf("admin may create restaurants: %v", err)
	}
}

func TestUpdateRestaurantOwnership(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService()
	o := owner()
	r := mustRestaurant(t, svc, o, "Марио")

	updated, err := svc.UpdateRestaurant(ctx, o, r.ID, domain.RestaurantPatch{Name: ptr("Марио и Луиджи")})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Марио и Луиджи" || updated.Address != r.Address {
		t.Fatalf("partial update must change only the name: %+v", updated)
	}
	if _, err := svc.UpdateRestaurant(ctx, owner(), r.ID, domain.RestaurantPatch{Name: ptr("Чужой")}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("foreign owner must be forbidden, got %v", err)
	}
	if _, err := svc.UpdateRestaurant(ctx, admin, r.ID, domain.RestaurantPatch{IsActive: ptr(false)}); err != nil {
		t.Fatalf("admin may update any restaurant: %v", err)
	}
	if _, err := svc.UpdateRestaurant(ctx, o, uuid.NewString(), domain.RestaurantPatch{}); !errors.Is(err, domain.ErrRestaurantNotFound) {
		t.Fatalf("unknown restaurant must be not found, got %v", err)
	}
}

func TestInactiveRestaurantsHiddenFromPublicList(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService()
	o := owner()
	mustRestaurant(t, svc, o, "Открытый")
	closed := mustRestaurant(t, svc, o, "Закрытый")
	if _, err := svc.UpdateRestaurant(ctx, o, closed.ID, domain.RestaurantPatch{IsActive: ptr(false)}); err != nil {
		t.Fatal(err)
	}

	public, total, err := svc.ListRestaurants(ctx, anonymous, domain.ListFilter{IncludeInactive: true})
	if err != nil || total != 1 || len(public) != 1 || public[0].Name != "Открытый" {
		t.Fatalf("public list must hide inactive restaurants: total=%d err=%v", total, err)
	}
	own, total, _ := svc.ListRestaurants(ctx, o, domain.ListFilter{OwnerID: o.UserID, IncludeInactive: true})
	if total != 2 || len(own) != 2 {
		t.Fatalf("owner must see own inactive restaurants, total=%d", total)
	}
	stranger := owner()
	foreign, total, _ := svc.ListRestaurants(ctx, stranger, domain.ListFilter{OwnerID: o.UserID, IncludeInactive: true})
	if total != 1 || len(foreign) != 1 {
		t.Fatalf("stranger must not see foreign inactive restaurants, total=%d", total)
	}
	all, total, _ := svc.ListRestaurants(ctx, admin, domain.ListFilter{IncludeInactive: true})
	if total != 2 || len(all) != 2 {
		t.Fatalf("admin must see all restaurants, total=%d", total)
	}
	if _, _, err := svc.ListRestaurants(ctx, anonymous, domain.ListFilter{OwnerID: "not-a-uuid"}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("invalid owner id must be rejected, got %v", err)
	}
}

func TestListRestaurantsFilters(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService()
	o := owner()
	mustRestaurant(t, svc, o, "Пиццерия Марио")
	r2, _ := svc.CreateRestaurant(ctx, o, domain.RestaurantInput{Name: "Сакура", Address: "Арбат", Cuisine: "Японская"})
	if r2 == nil {
		t.Fatal("restaurant not created")
	}
	byCuisine, _, _ := svc.ListRestaurants(ctx, anonymous, domain.ListFilter{Cuisine: "японская"})
	if len(byCuisine) != 1 || byCuisine[0].ID != r2.ID {
		t.Fatalf("cuisine filter must be case-insensitive: %d", len(byCuisine))
	}
	byQuery, _, _ := svc.ListRestaurants(ctx, anonymous, domain.ListFilter{Query: "марио"})
	if len(byQuery) != 1 {
		t.Fatalf("query filter must match by name: %d", len(byQuery))
	}
}

func TestMenuManagementOwnership(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService()
	o := owner()
	r := mustRestaurant(t, svc, o, "Марио")
	item := mustItem(t, svc, o, r.ID, "Маргарита", 549_00)

	stranger := owner()
	if _, err := svc.CreateMenuItem(ctx, stranger, r.ID, domain.MenuItemInput{Name: "X", Price: 100}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("stranger must not add items, got %v", err)
	}
	if _, err := svc.UpdateMenuItem(ctx, stranger, r.ID, item.ID, domain.MenuItemPatch{Price: ptr(int64(1))}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("stranger must not update items, got %v", err)
	}
	if err := svc.DeleteMenuItem(ctx, stranger, r.ID, item.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("stranger must not delete items, got %v", err)
	}
	if _, err := svc.CreateMenuItem(ctx, o, r.ID, domain.MenuItemInput{Name: "Бесплатно", Price: 0}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("zero price must be rejected, got %v", err)
	}
	if _, err := svc.CreateMenuItem(ctx, o, r.ID, domain.MenuItemInput{Name: "Дорого", Price: domain.MaxPrice + 1}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("too high price must be rejected, got %v", err)
	}

	updated, err := svc.UpdateMenuItem(ctx, o, r.ID, item.ID, domain.MenuItemPatch{IsAvailable: ptr(false), Price: ptr(int64(599_00))})
	if err != nil || updated.IsAvailable || updated.Price != 599_00 {
		t.Fatalf("owner must update item: %+v %v", updated, err)
	}
	available, err := svc.ListMenu(ctx, r.ID, true)
	if err != nil || len(available) != 0 {
		t.Fatalf("unavailable item must be hidden with only_available: %d %v", len(available), err)
	}
	all, _ := svc.ListMenu(ctx, r.ID, false)
	if len(all) != 1 {
		t.Fatalf("full menu must contain the item: %d", len(all))
	}

	if err := svc.DeleteMenuItem(ctx, admin, r.ID, item.ID); err != nil {
		t.Fatalf("admin may delete items: %v", err)
	}
	if err := svc.DeleteMenuItem(ctx, o, r.ID, item.ID); !errors.Is(err, domain.ErrMenuItemNotFound) {
		t.Fatalf("deleted item must be not found, got %v", err)
	}
	if _, err := svc.ListMenu(ctx, uuid.NewString(), false); !errors.Is(err, domain.ErrRestaurantNotFound) {
		t.Fatalf("menu of unknown restaurant must be not found, got %v", err)
	}
}

func TestGetMenuItems(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService()
	o := owner()
	r := mustRestaurant(t, svc, o, "Марио")
	pizza := mustItem(t, svc, o, r.ID, "Маргарита", 549_00)
	cola := mustItem(t, svc, o, r.ID, "Лимонад", 199_00)
	other := mustRestaurant(t, svc, o, "Другой")
	foreign := mustItem(t, svc, o, other.ID, "Чужая пицца", 100_00)

	rest, items, err := svc.GetMenuItems(ctx, r.ID, []string{pizza.ID, cola.ID, foreign.ID, uuid.NewString(), "garbage", pizza.ID})
	if err != nil {
		t.Fatal(err)
	}
	if rest.ID != r.ID || rest.OwnerID != o.UserID {
		t.Fatalf("unexpected restaurant: %+v", rest)
	}
	if len(items) != 2 {
		t.Fatalf("only existing items of this restaurant must be returned, got %d", len(items))
	}
	for _, it := range items {
		if it.RestaurantID != r.ID {
			t.Fatalf("item of another restaurant leaked: %+v", it)
		}
	}
	if _, _, err := svc.GetMenuItems(ctx, uuid.NewString(), []string{pizza.ID}); !errors.Is(err, domain.ErrRestaurantNotFound) {
		t.Fatalf("unknown restaurant must be not found, got %v", err)
	}
	_, none, err := svc.GetMenuItems(ctx, r.ID, nil)
	if err != nil || len(none) != 0 {
		t.Fatalf("empty request must return no items: %v", err)
	}
	tooMany := make([]string, MaxMenuItemsPerRequest+1)
	if _, _, err := svc.GetMenuItems(ctx, r.ID, tooMany); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("too many ids must be rejected, got %v", err)
	}
}

func TestSeedDemoIsIdempotent(t *testing.T) {
	ctx := context.Background()
	svc, repo := newService()
	added, err := svc.SeedDemo(ctx)
	if err != nil || added != len(demo.Restaurants) {
		t.Fatalf("first seed: added=%d err=%v", added, err)
	}
	added, err = svc.SeedDemo(ctx)
	if err != nil || added != 0 {
		t.Fatalf("second seed must add nothing: added=%d err=%v", added, err)
	}
	wantItems := 0
	for _, d := range demo.Restaurants {
		wantItems += len(d.Menu)
	}
	if len(repo.items) != wantItems {
		t.Fatalf("menu items = %d, want %d", len(repo.items), wantItems)
	}
	mario, err := svc.GetRestaurant(ctx, demo.Restaurants[0].ID)
	if err != nil || mario.OwnerID != demo.MarioID {
		t.Fatalf("demo restaurant must belong to demo owner: %v", err)
	}
}
