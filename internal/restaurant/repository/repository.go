// Package repository — слой доступа к данным Restaurant Service (PostgreSQL, pgx).
package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thaperfectcell/food-delivery/internal/restaurant/domain"
	"github.com/thaperfectcell/food-delivery/internal/restaurant/service"
	"github.com/thaperfectcell/food-delivery/pkg/postgres"
)

// Repository реализует service.Repository.
type Repository struct {
	pool *pgxpool.Pool
}

var _ service.Repository = (*Repository)(nil)

// New создаёт репозиторий.
func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

const (
	restaurantColumns = `id, owner_id, name, description, address, cuisine, is_active, created_at, updated_at`
	menuItemColumns   = `id, restaurant_id, name, description, category, price, is_available, created_at, updated_at`
)

// ---------------------------------------------------------------- рестораны

// CreateRestaurant сохраняет новый ресторан.
func (r *Repository) CreateRestaurant(ctx context.Context, x *domain.Restaurant) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO restaurants (`+restaurantColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		x.ID, x.OwnerID, x.Name, x.Description, x.Address, x.Cuisine, x.IsActive, x.CreatedAt, x.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create restaurant: %w", err)
	}
	return nil
}

// GetRestaurant возвращает ресторан по идентификатору.
func (r *Repository) GetRestaurant(ctx context.Context, id string) (*domain.Restaurant, error) {
	return scanRestaurant(r.pool.QueryRow(ctx, `SELECT `+restaurantColumns+` FROM restaurants WHERE id = $1`, id))
}

// UpdateRestaurant сохраняет изменённые поля ресторана.
func (r *Repository) UpdateRestaurant(ctx context.Context, x *domain.Restaurant) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE restaurants
		SET name = $2, description = $3, address = $4, cuisine = $5, is_active = $6, updated_at = $7
		WHERE id = $1`,
		x.ID, x.Name, x.Description, x.Address, x.Cuisine, x.IsActive, x.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update restaurant: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrRestaurantNotFound
	}
	return nil
}

// ListRestaurants возвращает страницу ресторанов по фильтру и общее количество.
func (r *Repository) ListRestaurants(ctx context.Context, f domain.ListFilter) ([]*domain.Restaurant, int, error) {
	var (
		where []string
		args  []any
	)
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if !f.IncludeInactive {
		where = append(where, "is_active")
	}
	if f.OwnerID != "" {
		add("owner_id = $%d", f.OwnerID)
	}
	if f.Cuisine != "" {
		add("lower(cuisine) = lower($%d)", f.Cuisine)
	}
	if f.Query != "" {
		add("name ILIKE '%%' || $%d || '%%'", escapeLike(f.Query))
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM restaurants`+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count restaurants: %w", err)
	}

	args = append(args, f.Limit, f.Offset)
	query := fmt.Sprintf(`SELECT %s FROM restaurants%s ORDER BY name, id LIMIT $%d OFFSET $%d`,
		restaurantColumns, cond, len(args)-1, len(args))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list restaurants: %w", err)
	}
	defer rows.Close()
	var list []*domain.Restaurant
	for rows.Next() {
		x, err := scanRestaurant(rows)
		if err != nil {
			return nil, 0, err
		}
		list = append(list, x)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ---------------------------------------------------------------- меню

// ListMenu возвращает меню ресторана, упорядоченное по категории и названию.
func (r *Repository) ListMenu(ctx context.Context, restaurantID string, onlyAvailable bool) ([]*domain.MenuItem, error) {
	query := `SELECT ` + menuItemColumns + ` FROM menu_items WHERE restaurant_id = $1`
	if onlyAvailable {
		query += ` AND is_available`
	}
	query += ` ORDER BY category, name, id`
	return r.queryItems(ctx, query, restaurantID)
}

// GetMenuItem возвращает позицию меню ресторана.
func (r *Repository) GetMenuItem(ctx context.Context, restaurantID, id string) (*domain.MenuItem, error) {
	return scanMenuItem(r.pool.QueryRow(ctx,
		`SELECT `+menuItemColumns+` FROM menu_items WHERE id = $1 AND restaurant_id = $2`, id, restaurantID))
}

// GetMenuItemsByIDs возвращает существующие позиции ресторана из списка ids.
func (r *Repository) GetMenuItemsByIDs(ctx context.Context, restaurantID string, ids []string) ([]*domain.MenuItem, error) {
	return r.queryItems(ctx, `SELECT `+menuItemColumns+` FROM menu_items
		WHERE restaurant_id = $1 AND id = ANY($2::uuid[]) ORDER BY category, name, id`, restaurantID, ids)
}

// CreateMenuItem сохраняет новую позицию меню.
func (r *Repository) CreateMenuItem(ctx context.Context, m *domain.MenuItem) error {
	if err := insertMenuItem(ctx, r.pool, m, false); err != nil {
		return fmt.Errorf("create menu item: %w", err)
	}
	return nil
}

// UpdateMenuItem сохраняет изменённые поля позиции меню.
func (r *Repository) UpdateMenuItem(ctx context.Context, m *domain.MenuItem) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE menu_items
		SET name = $3, description = $4, category = $5, price = $6, is_available = $7, updated_at = $8
		WHERE id = $1 AND restaurant_id = $2`,
		m.ID, m.RestaurantID, m.Name, m.Description, m.Category, m.Price, m.IsAvailable, m.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update menu item: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrMenuItemNotFound
	}
	return nil
}

// DeleteMenuItem удаляет позицию меню ресторана.
func (r *Repository) DeleteMenuItem(ctx context.Context, restaurantID, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM menu_items WHERE id = $1 AND restaurant_id = $2`, id, restaurantID)
	if err != nil {
		return fmt.Errorf("delete menu item: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrMenuItemNotFound
	}
	return nil
}

// SeedRestaurant идемпотентно сохраняет ресторан и меню в одной транзакции.
func (r *Repository) SeedRestaurant(ctx context.Context, x *domain.Restaurant, items []*domain.MenuItem) (bool, error) {
	added := false
	err := postgres.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO restaurants (`+restaurantColumns+`)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (id) DO NOTHING`,
			x.ID, x.OwnerID, x.Name, x.Description, x.Address, x.Cuisine, x.IsActive, x.CreatedAt, x.UpdatedAt)
		if err != nil {
			return err
		}
		added = tag.RowsAffected() == 1
		for _, m := range items {
			if err := insertMenuItem(ctx, tx, m, true); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("seed restaurant %s: %w", x.ID, err)
	}
	return added, nil
}

// ---------------------------------------------------------------- helpers

func insertMenuItem(ctx context.Context, q postgres.Querier, m *domain.MenuItem, ignoreConflict bool) error {
	query := `INSERT INTO menu_items (` + menuItemColumns + `) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	if ignoreConflict {
		query += ` ON CONFLICT (id) DO NOTHING`
	}
	_, err := q.Exec(ctx, query,
		m.ID, m.RestaurantID, m.Name, m.Description, m.Category, m.Price, m.IsAvailable, m.CreatedAt, m.UpdatedAt)
	return err
}

func (r *Repository) queryItems(ctx context.Context, query string, args ...any) ([]*domain.MenuItem, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query menu items: %w", err)
	}
	defer rows.Close()
	var items []*domain.MenuItem
	for rows.Next() {
		m, err := scanMenuItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, m)
	}
	return items, rows.Err()
}

func scanRestaurant(row pgx.Row) (*domain.Restaurant, error) {
	var x domain.Restaurant
	err := row.Scan(&x.ID, &x.OwnerID, &x.Name, &x.Description, &x.Address, &x.Cuisine,
		&x.IsActive, &x.CreatedAt, &x.UpdatedAt)
	if err != nil {
		if postgres.IsNoRows(err) {
			return nil, domain.ErrRestaurantNotFound
		}
		return nil, fmt.Errorf("scan restaurant: %w", err)
	}
	return &x, nil
}

func scanMenuItem(row pgx.Row) (*domain.MenuItem, error) {
	var m domain.MenuItem
	err := row.Scan(&m.ID, &m.RestaurantID, &m.Name, &m.Description, &m.Category, &m.Price,
		&m.IsAvailable, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		if postgres.IsNoRows(err) {
			return nil, domain.ErrMenuItemNotFound
		}
		return nil, fmt.Errorf("scan menu item: %w", err)
	}
	return &m, nil
}

// escapeLike экранирует спецсимволы шаблона LIKE в пользовательском вводе.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
