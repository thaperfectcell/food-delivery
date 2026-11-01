// Package repository — слой доступа к данным Order Service (PostgreSQL, pgx).
package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thaperfectcell/food-delivery/internal/order/domain"
	"github.com/thaperfectcell/food-delivery/internal/order/service"
	"github.com/thaperfectcell/food-delivery/pkg/apperr"
	"github.com/thaperfectcell/food-delivery/pkg/events"
	"github.com/thaperfectcell/food-delivery/pkg/outbox"
	"github.com/thaperfectcell/food-delivery/pkg/postgres"
)

// Repository реализует service.Repository.
type Repository struct {
	pool *pgxpool.Pool
}

var _ service.Repository = (*Repository)(nil)

// New создаёт репозиторий.
func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

const orderColumns = `id, customer_id, restaurant_id, restaurant_owner_id, restaurant_name,
	restaurant_address, status, items_total, delivery_fee, total, delivery_address, comment,
	courier_id, courier_name, cancel_reason, COALESCE(idempotency_key, ''), created_at, updated_at`

// Create сохраняет новый заказ, его позиции, историю и события в одной транзакции.
func (r *Repository) Create(ctx context.Context, o *domain.Order, evts []events.Envelope) error {
	err := postgres.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO orders (id, customer_id, restaurant_id, restaurant_owner_id, restaurant_name,
				restaurant_address, status, items_total, delivery_fee, total, delivery_address, comment,
				idempotency_key, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
			o.ID, o.CustomerID, o.RestaurantID, o.RestaurantOwnerID, o.RestaurantName,
			o.RestaurantAddress, string(o.Status), o.ItemsTotal, o.DeliveryFee, o.Total,
			o.DeliveryAddress, o.Comment, postgres.NullString(o.IdempotencyKey), o.CreatedAt, o.UpdatedAt)
		if err != nil {
			return err
		}
		batch := &pgx.Batch{}
		for i, it := range o.Items {
			batch.Queue(`INSERT INTO order_items (order_id, position, menu_item_id, name, price, quantity)
				VALUES ($1, $2, $3, $4, $5, $6)`, o.ID, i+1, it.MenuItemID, it.Name, it.Price, it.Quantity)
		}
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return fmt.Errorf("insert order items: %w", err)
		}
		if err := insertHistory(ctx, tx, o); err != nil {
			return err
		}
		return outbox.Add(ctx, tx, evts...)
	})
	if err != nil {
		if postgres.IsUniqueViolation(err) {
			return apperr.Wrap(apperr.KindConflict, "заказ с таким ключом идемпотентности уже создан", err)
		}
		return fmt.Errorf("create order: %w", err)
	}
	o.ClearPending()
	return nil
}

// Get возвращает заказ с позициями и историей.
func (r *Repository) Get(ctx context.Context, id string) (*domain.Order, error) {
	o, err := scanOrder(r.pool.QueryRow(ctx, `SELECT `+orderColumns+` FROM orders WHERE id = $1`, id))
	if err != nil {
		return nil, err
	}
	if err := loadDetails(ctx, r.pool, o); err != nil {
		return nil, err
	}
	return o, nil
}

// GetByIdempotencyKey ищет заказ клиента по ключу идемпотентности.
func (r *Repository) GetByIdempotencyKey(ctx context.Context, customerID, key string) (*domain.Order, error) {
	o, err := scanOrder(r.pool.QueryRow(ctx,
		`SELECT `+orderColumns+` FROM orders WHERE customer_id = $1 AND idempotency_key = $2`, customerID, key))
	if err != nil {
		return nil, err
	}
	if err := loadDetails(ctx, r.pool, o); err != nil {
		return nil, err
	}
	return o, nil
}

// List возвращает страницу заказов по фильтру и общее количество.
func (r *Repository) List(ctx context.Context, f domain.ListFilter) ([]*domain.Order, int, error) {
	var (
		where []string
		args  []any
	)
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.CustomerID != "" {
		add("customer_id = $%d", f.CustomerID)
	}
	if f.RestaurantOwnerID != "" {
		add("restaurant_owner_id = $%d", f.RestaurantOwnerID)
	}
	if f.RestaurantID != "" {
		add("restaurant_id = $%d", f.RestaurantID)
	}
	if len(f.Statuses) > 0 {
		st := make([]string, len(f.Statuses))
		for i, s := range f.Statuses {
			st[i] = string(s)
		}
		add("status = ANY($%d)", st)
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM orders`+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count orders: %w", err)
	}

	args = append(args, f.Limit, f.Offset)
	rows, err := r.pool.Query(ctx, fmt.Sprintf(`SELECT %s FROM orders%s ORDER BY created_at DESC LIMIT $%d OFFSET $%d`,
		orderColumns, cond, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list orders: %w", err)
	}
	var list []*domain.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		list = append(list, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if err := loadItemsBatch(ctx, r.pool, list); err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// Update блокирует строку заказа и применяет изменения в одной транзакции.
func (r *Repository) Update(ctx context.Context, id string, fn service.MutateFunc) (*domain.Order, error) {
	var result *domain.Order
	err := postgres.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		o, err := r.mutateLocked(ctx, tx, id, fn)
		result = o
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// UpdateOnce применяет изменения не более одного раза для события eventID:
// запись в processed_events и изменение заказа фиксируются атомарно.
func (r *Repository) UpdateOnce(ctx context.Context, eventID, eventType, orderID string, fn service.MutateFunc) (bool, error) {
	applied := false
	err := postgres.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`INSERT INTO processed_events (event_id, event_type) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			eventID, eventType)
		if err != nil {
			return fmt.Errorf("mark event processed: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return nil // событие уже обработано
		}
		if _, err := r.mutateLocked(ctx, tx, orderID, fn); err != nil {
			return err
		}
		applied = true
		return nil
	})
	return applied, err
}

func (r *Repository) mutateLocked(ctx context.Context, tx pgx.Tx, id string, fn service.MutateFunc) (*domain.Order, error) {
	o, err := scanOrder(tx.QueryRow(ctx, `SELECT `+orderColumns+` FROM orders WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return nil, err
	}
	if err := loadDetails(ctx, tx, o); err != nil {
		return nil, err
	}
	evts, err := fn(o)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE orders SET status = $2, courier_id = $3, courier_name = $4, cancel_reason = $5, updated_at = $6
		WHERE id = $1`,
		o.ID, string(o.Status), postgres.NullString(o.CourierID), o.CourierName, o.CancelReason, o.UpdatedAt); err != nil {
		return nil, fmt.Errorf("update order: %w", err)
	}
	if err := insertHistory(ctx, tx, o); err != nil {
		return nil, err
	}
	if err := outbox.Add(ctx, tx, evts...); err != nil {
		return nil, err
	}
	o.ClearPending()
	return o, nil
}

// Stats рассчитывает агрегированную статистику средствами SQL.
func (r *Repository) Stats(ctx context.Context, since time.Time) (domain.Stats, error) {
	st := domain.Stats{ByStatus: map[domain.Status]int64{}}
	rows, err := r.pool.Query(ctx, `SELECT status, count(*) FROM orders GROUP BY status`)
	if err != nil {
		return st, fmt.Errorf("stats by status: %w", err)
	}
	for rows.Next() {
		var s string
		var n int64
		if err := rows.Scan(&s, &n); err != nil {
			rows.Close()
			return st, err
		}
		st.ByStatus[domain.Status(s)] = n
		st.TotalOrders += n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}
	err = r.pool.QueryRow(ctx, `
		SELECT COALESCE(sum(total), 0)::bigint, COALESCE(avg(total), 0)::bigint
		FROM orders WHERE status = 'delivered'`).Scan(&st.Revenue, &st.AverageCheck)
	if err != nil {
		return st, fmt.Errorf("stats revenue: %w", err)
	}
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE created_at >= $1`, since).Scan(&st.OrdersToday); err != nil {
		return st, fmt.Errorf("stats today: %w", err)
	}
	return st, nil
}

// ---------------------------------------------------------------- helpers

func scanOrder(row pgx.Row) (*domain.Order, error) {
	var (
		o         domain.Order
		status    string
		courierID *string
	)
	err := row.Scan(&o.ID, &o.CustomerID, &o.RestaurantID, &o.RestaurantOwnerID, &o.RestaurantName,
		&o.RestaurantAddress, &status, &o.ItemsTotal, &o.DeliveryFee, &o.Total, &o.DeliveryAddress,
		&o.Comment, &courierID, &o.CourierName, &o.CancelReason, &o.IdempotencyKey, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		if postgres.IsNoRows(err) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scan order: %w", err)
	}
	o.Status = domain.Status(status)
	if courierID != nil {
		o.CourierID = *courierID
	}
	return &o, nil
}

func loadDetails(ctx context.Context, q postgres.Querier, o *domain.Order) error {
	if err := loadItemsBatch(ctx, q, []*domain.Order{o}); err != nil {
		return err
	}
	rows, err := q.Query(ctx,
		`SELECT status, reason, changed_at FROM order_status_history WHERE order_id = $1 ORDER BY id`, o.ID)
	if err != nil {
		return fmt.Errorf("load history: %w", err)
	}
	defer rows.Close()
	o.History = nil
	for rows.Next() {
		var (
			ch domain.StatusChange
			s  string
		)
		if err := rows.Scan(&s, &ch.Reason, &ch.At); err != nil {
			return err
		}
		ch.Status = domain.Status(s)
		o.History = append(o.History, ch)
	}
	return rows.Err()
}

// loadItemsBatch загружает позиции для нескольких заказов одним запросом.
func loadItemsBatch(ctx context.Context, q postgres.Querier, list []*domain.Order) error {
	if len(list) == 0 {
		return nil
	}
	ids := make([]string, len(list))
	byID := make(map[string]*domain.Order, len(list))
	for i, o := range list {
		ids[i] = o.ID
		byID[o.ID] = o
		o.Items = nil
	}
	rows, err := q.Query(ctx, `
		SELECT order_id, menu_item_id, name, price, quantity
		FROM order_items WHERE order_id = ANY($1::uuid[]) ORDER BY order_id, position`, ids)
	if err != nil {
		return fmt.Errorf("load items: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			orderID string
			it      domain.Item
		)
		if err := rows.Scan(&orderID, &it.MenuItemID, &it.Name, &it.Price, &it.Quantity); err != nil {
			return err
		}
		if o := byID[orderID]; o != nil {
			o.Items = append(o.Items, it)
		}
	}
	return rows.Err()
}

func insertHistory(ctx context.Context, tx pgx.Tx, o *domain.Order) error {
	for _, ch := range o.PendingChanges() {
		if _, err := tx.Exec(ctx,
			`INSERT INTO order_status_history (order_id, status, reason, changed_at) VALUES ($1, $2, $3, $4)`,
			o.ID, string(ch.Status), ch.Reason, ch.At); err != nil {
			return fmt.Errorf("insert status history: %w", err)
		}
	}
	return nil
}
