// Package service — слой бизнес-логики Restaurant Service: управление
// ресторанами и меню, публичный каталог и проверка позиций для Order Service.
package service

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thaperfectcell/food-delivery/internal/restaurant/domain"
	"github.com/thaperfectcell/food-delivery/pkg/apperr"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
	"github.com/thaperfectcell/food-delivery/pkg/demo"
)

// MaxMenuItemsPerRequest — ограничение числа позиций в запросе GetMenuItems.
const MaxMenuItemsPerRequest = 100

// Repository — порт хранилища ресторанов и меню.
type Repository interface {
	CreateRestaurant(ctx context.Context, r *domain.Restaurant) error
	GetRestaurant(ctx context.Context, id string) (*domain.Restaurant, error)
	UpdateRestaurant(ctx context.Context, r *domain.Restaurant) error
	ListRestaurants(ctx context.Context, f domain.ListFilter) ([]*domain.Restaurant, int, error)

	ListMenu(ctx context.Context, restaurantID string, onlyAvailable bool) ([]*domain.MenuItem, error)
	GetMenuItem(ctx context.Context, restaurantID, id string) (*domain.MenuItem, error)
	GetMenuItemsByIDs(ctx context.Context, restaurantID string, ids []string) ([]*domain.MenuItem, error)
	CreateMenuItem(ctx context.Context, m *domain.MenuItem) error
	UpdateMenuItem(ctx context.Context, m *domain.MenuItem) error
	DeleteMenuItem(ctx context.Context, restaurantID, id string) error

	// SeedRestaurant идемпотентно сохраняет ресторан и его меню
	// (существующие записи не изменяются). Возвращает true, если ресторан добавлен.
	SeedRestaurant(ctx context.Context, r *domain.Restaurant, items []*domain.MenuItem) (bool, error)
}

// Service реализует сценарии использования Restaurant Service.
type Service struct {
	repo  Repository
	log   *slog.Logger
	now   func() time.Time
	newID func() string
}

// New создаёт сервис.
func New(repo Repository, log *slog.Logger) *Service {
	return &Service{
		repo:  repo,
		log:   log,
		now:   func() time.Time { return time.Now().UTC() },
		newID: uuid.NewString,
	}
}

// ---------------------------------------------------------------- рестораны

// CreateRestaurant создаёт ресторан; владельцем становится вызывающий.
func (s *Service) CreateRestaurant(ctx context.Context, actor auth.Identity, in domain.RestaurantInput) (*domain.Restaurant, error) {
	if actor.IsZero() || !actor.Is(auth.RoleRestaurant, auth.RoleAdmin) {
		return nil, apperr.Forbidden("создавать рестораны могут только партнёры-рестораны")
	}
	r, err := domain.NewRestaurant(s.newID(), actor.UserID, in, s.now())
	if err != nil {
		return nil, err
	}
	if err := s.repo.CreateRestaurant(ctx, r); err != nil {
		return nil, err
	}
	s.log.InfoContext(ctx, "restaurant created", slog.String("restaurant_id", r.ID), slog.String("owner_id", r.OwnerID))
	return r, nil
}

// UpdateRestaurant изменяет ресторан (владелец или администратор).
func (s *Service) UpdateRestaurant(ctx context.Context, actor auth.Identity, id string, p domain.RestaurantPatch) (*domain.Restaurant, error) {
	r, err := s.ownedRestaurant(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	if err := r.Apply(p, s.now()); err != nil {
		return nil, err
	}
	if err := s.repo.UpdateRestaurant(ctx, r); err != nil {
		return nil, err
	}
	s.log.InfoContext(ctx, "restaurant updated", slog.String("restaurant_id", r.ID))
	return r, nil
}

// GetRestaurant возвращает ресторан (публичная операция).
func (s *Service) GetRestaurant(ctx context.Context, id string) (*domain.Restaurant, error) {
	if !isUUID(id) {
		return nil, domain.ErrRestaurantNotFound
	}
	return s.repo.GetRestaurant(ctx, id)
}

// ListRestaurants возвращает каталог. Публично видны только активные рестораны;
// неактивные — владельцу (фильтр по себе) и администратору.
func (s *Service) ListRestaurants(ctx context.Context, actor auth.Identity, f domain.ListFilter) ([]*domain.Restaurant, int, error) {
	f.OwnerID = strings.TrimSpace(f.OwnerID)
	if f.OwnerID != "" && !isUUID(f.OwnerID) {
		return nil, 0, apperr.Invalid("некорректный идентификатор владельца")
	}
	ownList := f.OwnerID != "" && !actor.IsZero() && f.OwnerID == actor.UserID
	f.IncludeInactive = f.IncludeInactive && (actor.Privileged() || ownList)
	f.Cuisine = strings.TrimSpace(f.Cuisine)
	f.Query = strings.TrimSpace(f.Query)
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	return s.repo.ListRestaurants(ctx, f)
}

// ---------------------------------------------------------------- меню

// ListMenu возвращает меню ресторана (публичная операция).
func (s *Service) ListMenu(ctx context.Context, restaurantID string, onlyAvailable bool) ([]*domain.MenuItem, error) {
	if _, err := s.GetRestaurant(ctx, restaurantID); err != nil {
		return nil, err
	}
	return s.repo.ListMenu(ctx, restaurantID, onlyAvailable)
}

// CreateMenuItem добавляет позицию в меню (владелец или администратор).
func (s *Service) CreateMenuItem(ctx context.Context, actor auth.Identity, restaurantID string, in domain.MenuItemInput) (*domain.MenuItem, error) {
	if _, err := s.ownedRestaurant(ctx, actor, restaurantID); err != nil {
		return nil, err
	}
	m, err := domain.NewMenuItem(s.newID(), restaurantID, in, s.now())
	if err != nil {
		return nil, err
	}
	if err := s.repo.CreateMenuItem(ctx, m); err != nil {
		return nil, err
	}
	s.log.InfoContext(ctx, "menu item created", slog.String("restaurant_id", restaurantID), slog.String("item_id", m.ID))
	return m, nil
}

// UpdateMenuItem изменяет позицию меню (цена, доступность и т.д.).
func (s *Service) UpdateMenuItem(ctx context.Context, actor auth.Identity, restaurantID, itemID string, p domain.MenuItemPatch) (*domain.MenuItem, error) {
	if _, err := s.ownedRestaurant(ctx, actor, restaurantID); err != nil {
		return nil, err
	}
	if !isUUID(itemID) {
		return nil, domain.ErrMenuItemNotFound
	}
	m, err := s.repo.GetMenuItem(ctx, restaurantID, itemID)
	if err != nil {
		return nil, err
	}
	if err := m.Apply(p, s.now()); err != nil {
		return nil, err
	}
	if err := s.repo.UpdateMenuItem(ctx, m); err != nil {
		return nil, err
	}
	s.log.InfoContext(ctx, "menu item updated", slog.String("restaurant_id", restaurantID), slog.String("item_id", m.ID))
	return m, nil
}

// DeleteMenuItem удаляет позицию меню. Уже оформленные заказы не затрагиваются:
// Order Service хранит снимок названия и цены позиции.
func (s *Service) DeleteMenuItem(ctx context.Context, actor auth.Identity, restaurantID, itemID string) error {
	if _, err := s.ownedRestaurant(ctx, actor, restaurantID); err != nil {
		return err
	}
	if !isUUID(itemID) {
		return domain.ErrMenuItemNotFound
	}
	if err := s.repo.DeleteMenuItem(ctx, restaurantID, itemID); err != nil {
		return err
	}
	s.log.InfoContext(ctx, "menu item deleted", slog.String("restaurant_id", restaurantID), slog.String("item_id", itemID))
	return nil
}

// GetMenuItems возвращает ресторан и запрошенные позиции его меню с
// актуальными ценами и доступностью (используется Order Service при
// оформлении заказа). Неизвестные идентификаторы молча пропускаются —
// решение о недопустимости заказа принимает Order Service.
func (s *Service) GetMenuItems(ctx context.Context, restaurantID string, ids []string) (*domain.Restaurant, []*domain.MenuItem, error) {
	r, err := s.GetRestaurant(ctx, restaurantID)
	if err != nil {
		return nil, nil, err
	}
	if len(ids) > MaxMenuItemsPerRequest {
		return nil, nil, apperr.Invalid("слишком много позиций в запросе")
	}
	seen := make(map[string]bool, len(ids))
	valid := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if !isUUID(id) || seen[id] {
			continue
		}
		seen[id] = true
		valid = append(valid, id)
	}
	if len(valid) == 0 {
		return r, nil, nil
	}
	items, err := s.repo.GetMenuItemsByIDs(ctx, restaurantID, valid)
	if err != nil {
		return nil, nil, err
	}
	return r, items, nil
}

// ---------------------------------------------------------------- начальные данные

// SeedDemo идемпотентно создаёт демонстрационные рестораны и меню (pkg/demo).
// Возвращает количество добавленных ресторанов.
func (s *Service) SeedDemo(ctx context.Context) (int, error) {
	now := s.now()
	added := 0
	for _, d := range demo.Restaurants {
		r, err := domain.NewRestaurant(d.ID, d.OwnerID, domain.RestaurantInput{
			Name:        d.Name,
			Description: d.Description,
			Address:     d.Address,
			Cuisine:     d.Cuisine,
		}, now)
		if err != nil {
			return added, err
		}
		items := make([]*domain.MenuItem, 0, len(d.Menu))
		for i, it := range d.Menu {
			// Разносим created_at, чтобы порядок позиций был стабильным.
			m, err := domain.NewMenuItem(it.ID, d.ID, domain.MenuItemInput{
				Name:        it.Name,
				Description: it.Description,
				Category:    it.Category,
				Price:       it.Price,
			}, now.Add(time.Duration(i)*time.Millisecond))
			if err != nil {
				return added, err
			}
			items = append(items, m)
		}
		ok, err := s.repo.SeedRestaurant(ctx, r, items)
		if err != nil {
			return added, err
		}
		if ok {
			added++
		}
	}
	return added, nil
}

// ---------------------------------------------------------------- вспомогательное

// ownedRestaurant загружает ресторан и проверяет, что вызывающий — его
// владелец (роль restaurant) или администратор.
func (s *Service) ownedRestaurant(ctx context.Context, actor auth.Identity, id string) (*domain.Restaurant, error) {
	if actor.IsZero() {
		return nil, apperr.Unauthenticated("требуется вход в систему")
	}
	r, err := s.GetRestaurant(ctx, id)
	if err != nil {
		return nil, err
	}
	if actor.Privileged() {
		return r, nil
	}
	if !actor.Is(auth.RoleRestaurant) || r.OwnerID != actor.UserID {
		return nil, domain.ErrForbidden
	}
	return r, nil
}

func isUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}
