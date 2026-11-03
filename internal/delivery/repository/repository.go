// Package repository — слой доступа к данным Delivery Service (PostgreSQL, pgx).
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thaperfectcell/food-delivery/internal/delivery/domain"
	"github.com/thaperfectcell/food-delivery/internal/delivery/service"
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

const (
	courierColumns = `id, name, status, completed_deliveries, status_changed_at, created_at`

	// deliverySelect — выборка доставки с именем назначенного курьера.
	deliverySelect = `SELECT d.id, d.order_id, d.customer_id, d.courier_id, COALESCE(c.name, ''), d.status,
		d.restaurant_name, d.restaurant_address, d.delivery_address, d.order_total, d.order_ready,
		d.created_at, d.assigned_at, d.picked_up_at, d.delivered_at, d.cancelled_at
	FROM deliveries d LEFT JOIN couriers c ON c.id = d.courier_id`
)

// ---------------------------------------------------------------- курьеры

// GetCourier возвращает профиль курьера.
func (r *Repository) GetCourier(ctx context.Context, id string) (*domain.Courier, error) {
	return scanCourier(r.pool.QueryRow(ctx, `SELECT `+courierColumns+` FROM couriers WHERE id = $1`, id))
}

// UpsertCourier создаёт профиль при первом обращении, блокирует его и применяет fn.
func (r *Repository) UpsertCourier(ctx context.Context, id, name string, now time.Time, fn service.CourierMutateFunc) (*domain.Courier, error) {
	insertName := name
	if insertName == "" {
		insertName = domain.NewCourier(id, "", now).Name
	}
	var result *domain.Courier
	err := postgres.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO couriers (id, name, status, completed_deliveries, status_changed_at, created_at)
			VALUES ($1, $2, 'offline', 0, $3, $3)
			ON CONFLICT (id) DO UPDATE
			SET name = CASE WHEN $4::text <> '' THEN $4::text ELSE couriers.name END`,
			id, insertName, now, name); err != nil {
			return fmt.Errorf("upsert courier: %w", err)
		}
		c, err := scanCourier(tx.QueryRow(ctx, `SELECT `+courierColumns+` FROM couriers WHERE id = $1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if err := fn(c); err != nil {
			return err
		}
		if err := saveCourier(ctx, tx, c); err != nil {
			return err
		}
		result = c
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ---------------------------------------------------------------- доставки

// GetDelivery возвращает доставку по идентификатору.
func (r *Repository) GetDelivery(ctx context.Context, id string) (*domain.Delivery, error) {
	return scanDelivery(r.pool.QueryRow(ctx, deliverySelect+` WHERE d.id = $1`, id))
}

// GetDeliveryByOrder возвращает доставку заказа.
func (r *Repository) GetDeliveryByOrder(ctx context.Context, orderID string) (*domain.Delivery, error) {
	return scanDelivery(r.pool.QueryRow(ctx, deliverySelect+` WHERE d.order_id = $1`, orderID))
}

// ListCourierDeliveries возвращает доставки курьера (новые первыми).
func (r *Repository) ListCourierDeliveries(ctx context.Context, courierID string, activeOnly bool, limit int) ([]*domain.Delivery, error) {
	query := deliverySelect + ` WHERE d.courier_id = $1`
	if activeOnly {
		query += ` AND d.status IN ('assigned', 'picked_up')`
	}
	query += ` ORDER BY d.created_at DESC LIMIT $2`
	rows, err := r.pool.Query(ctx, query, courierID, limit)
	if err != nil {
		return nil, fmt.Errorf("list courier deliveries: %w", err)
	}
	defer rows.Close()
	var list []*domain.Delivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	return list, rows.Err()
}

// CreateOnce отмечает событие обработанным и сохраняет новую доставку.
func (r *Repository) CreateOnce(ctx context.Context, eventID, eventType string, d *domain.Delivery) (bool, error) {
	created := false
	err := postgres.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		ok, err := markProcessed(ctx, tx, eventID, eventType)
		if err != nil || !ok {
			return err
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO deliveries (id, order_id, customer_id, status, restaurant_name, restaurant_address,
				delivery_address, order_total, order_ready, created_at, cancelled_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (order_id) DO NOTHING`,
			d.ID, d.OrderID, d.CustomerID, string(d.Status), d.RestaurantName, d.RestaurantAddress,
			d.DeliveryAddress, d.OrderTotal, d.OrderReady, d.CreatedAt, postgres.NullTime(d.CancelledAt))
		if err != nil {
			return fmt.Errorf("insert delivery: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrDeliveryExists // откат, включая отметку события
		}
		created = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return created, nil
}

// UpdateDelivery блокирует доставку и назначенного курьера и применяет fn.
func (r *Repository) UpdateDelivery(ctx context.Context, id string, fn service.DeliveryMutateFunc) (*domain.Delivery, error) {
	var result *domain.Delivery
	err := postgres.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		d, err := r.mutateLocked(ctx, tx, `d.id = $1`, id, fn)
		result = d
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// UpdateByOrderOnce применяет fn к доставке заказа не более одного раза для события.
func (r *Repository) UpdateByOrderOnce(ctx context.Context, eventID, eventType, orderID string, fn service.DeliveryMutateFunc) (bool, error) {
	applied := false
	err := postgres.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		ok, err := markProcessed(ctx, tx, eventID, eventType)
		if err != nil || !ok {
			return err
		}
		if _, err := r.mutateLocked(ctx, tx, `d.order_id = $1`, orderID, fn); err != nil {
			return err
		}
		applied = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

// AssignNext блокирует самую раннюю ожидающую доставку и свободного курьера,
// дольше всех ожидающего заказа. SKIP LOCKED позволяет нескольким экземплярам
// сервиса назначать курьеров параллельно без двойного назначения.
func (r *Repository) AssignNext(ctx context.Context, fn service.DeliveryMutateFunc) (bool, error) {
	assigned := false
	err := postgres.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		d, err := scanDelivery(tx.QueryRow(ctx, deliverySelect+`
			WHERE d.status = 'pending'
			ORDER BY d.created_at, d.id
			LIMIT 1
			FOR UPDATE OF d SKIP LOCKED`))
		if err != nil {
			if errors.Is(err, domain.ErrDeliveryNotFound) {
				return nil // очередь пуста
			}
			return err
		}
		c, err := scanCourier(tx.QueryRow(ctx, `
			SELECT `+courierColumns+` FROM couriers
			WHERE status = 'available'
			ORDER BY status_changed_at, id
			LIMIT 1
			FOR UPDATE SKIP LOCKED`))
		if err != nil {
			if errors.Is(err, domain.ErrCourierNotFound) {
				return nil // свободных курьеров нет
			}
			return err
		}
		evts, err := fn(d, c)
		if err != nil {
			return err
		}
		if err := saveDelivery(ctx, tx, d); err != nil {
			return err
		}
		if err := saveCourier(ctx, tx, c); err != nil {
			return err
		}
		if err := outbox.Add(ctx, tx, evts...); err != nil {
			return err
		}
		assigned = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return assigned, nil
}

// Stats рассчитывает сводную статистику средствами SQL.
func (r *Repository) Stats(ctx context.Context) (domain.Stats, error) {
	var st domain.Stats
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status IN ('available', 'busy')),
		       count(*) FILTER (WHERE status = 'busy')
		FROM couriers`).Scan(&st.CouriersOnline, &st.CouriersBusy); err != nil {
		return st, fmt.Errorf("courier stats: %w", err)
	}
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'pending'),
		       count(*) FILTER (WHERE status IN ('assigned', 'picked_up')),
		       count(*) FILTER (WHERE status = 'delivered')
		FROM deliveries`).Scan(&st.DeliveriesPending, &st.DeliveriesActive, &st.DeliveriesCompleted); err != nil {
		return st, fmt.Errorf("delivery stats: %w", err)
	}
	return st, nil
}

// ---------------------------------------------------------------- helpers

// mutateLocked блокирует доставку (условие where с параметром $1) и её курьера,
// применяет fn и сохраняет изменения и события. Порядок блокировок —
// «доставка, затем курьер» — единый для всех операций, что исключает взаимоблокировки.
func (r *Repository) mutateLocked(ctx context.Context, tx pgx.Tx, where, arg string, fn service.DeliveryMutateFunc) (*domain.Delivery, error) {
	d, err := scanDelivery(tx.QueryRow(ctx, deliverySelect+` WHERE `+where+` FOR UPDATE OF d`, arg))
	if err != nil {
		return nil, err
	}
	var c *domain.Courier
	if d.CourierID != "" {
		c, err = scanCourier(tx.QueryRow(ctx, `SELECT `+courierColumns+` FROM couriers WHERE id = $1 FOR UPDATE`, d.CourierID))
		if err != nil {
			return nil, err
		}
	}
	evts, err := fn(d, c)
	if err != nil {
		return nil, err
	}
	if err := saveDelivery(ctx, tx, d); err != nil {
		return nil, err
	}
	if c != nil {
		if err := saveCourier(ctx, tx, c); err != nil {
			return nil, err
		}
	}
	if err := outbox.Add(ctx, tx, evts...); err != nil {
		return nil, err
	}
	return d, nil
}

func saveDelivery(ctx context.Context, tx pgx.Tx, d *domain.Delivery) error {
	_, err := tx.Exec(ctx, `
		UPDATE deliveries
		SET courier_id = $2, status = $3, order_ready = $4,
		    assigned_at = $5, picked_up_at = $6, delivered_at = $7, cancelled_at = $8
		WHERE id = $1`,
		d.ID, postgres.NullString(d.CourierID), string(d.Status), d.OrderReady,
		postgres.NullTime(d.AssignedAt), postgres.NullTime(d.PickedUpAt),
		postgres.NullTime(d.DeliveredAt), postgres.NullTime(d.CancelledAt))
	if err != nil {
		return fmt.Errorf("update delivery: %w", err)
	}
	return nil
}

func saveCourier(ctx context.Context, tx pgx.Tx, c *domain.Courier) error {
	_, err := tx.Exec(ctx, `
		UPDATE couriers SET name = $2, status = $3, completed_deliveries = $4, status_changed_at = $5
		WHERE id = $1`,
		c.ID, c.Name, string(c.Status), c.CompletedDeliveries, c.StatusChangedAt)
	if err != nil {
		return fmt.Errorf("update courier: %w", err)
	}
	return nil
}

// markProcessed записывает событие в processed_events; false — уже обработано.
func markProcessed(ctx context.Context, tx pgx.Tx, eventID, eventType string) (bool, error) {
	tag, err := tx.Exec(ctx,
		`INSERT INTO processed_events (event_id, event_type) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		eventID, eventType)
	if err != nil {
		return false, fmt.Errorf("mark event processed: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func scanCourier(row pgx.Row) (*domain.Courier, error) {
	var (
		c      domain.Courier
		status string
	)
	if err := row.Scan(&c.ID, &c.Name, &status, &c.CompletedDeliveries, &c.StatusChangedAt, &c.CreatedAt); err != nil {
		if postgres.IsNoRows(err) {
			return nil, domain.ErrCourierNotFound
		}
		return nil, fmt.Errorf("scan courier: %w", err)
	}
	c.Status = domain.CourierStatus(status)
	return &c, nil
}

func scanDelivery(row pgx.Row) (*domain.Delivery, error) {
	var (
		d                                         domain.Delivery
		status                                    string
		courierID                                 *string
		assignedAt, pickedUpAt, delivered, cancel *time.Time
	)
	err := row.Scan(&d.ID, &d.OrderID, &d.CustomerID, &courierID, &d.CourierName, &status,
		&d.RestaurantName, &d.RestaurantAddress, &d.DeliveryAddress, &d.OrderTotal, &d.OrderReady,
		&d.CreatedAt, &assignedAt, &pickedUpAt, &delivered, &cancel)
	if err != nil {
		if postgres.IsNoRows(err) {
			return nil, domain.ErrDeliveryNotFound
		}
		return nil, fmt.Errorf("scan delivery: %w", err)
	}
	d.Status = domain.Status(status)
	if courierID != nil {
		d.CourierID = *courierID
	}
	d.AssignedAt = deref(assignedAt)
	d.PickedUpAt = deref(pickedUpAt)
	d.DeliveredAt = deref(delivered)
	d.CancelledAt = deref(cancel)
	return &d, nil
}

func deref(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
