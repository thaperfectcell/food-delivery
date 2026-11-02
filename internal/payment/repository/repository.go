// Package repository — слой доступа к данным Payment Service (PostgreSQL, pgx).
package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thaperfectcell/food-delivery/internal/payment/domain"
	"github.com/thaperfectcell/food-delivery/internal/payment/service"
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

const paymentColumns = `id, order_id, customer_id, amount, status, failure_reason, provider_ref, created_at, updated_at`

// GetByOrder возвращает платёж по идентификатору заказа.
func (r *Repository) GetByOrder(ctx context.Context, orderID string) (*domain.Payment, error) {
	return scanPayment(r.pool.QueryRow(ctx, `SELECT `+paymentColumns+` FROM payments WHERE order_id = $1`, orderID))
}

// List возвращает страницу платежей (новые первыми) и общее количество.
func (r *Repository) List(ctx context.Context, limit, offset int) ([]*domain.Payment, int, error) {
	var total int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM payments`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count payments: %w", err)
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+paymentColumns+` FROM payments ORDER BY created_at DESC, id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list payments: %w", err)
	}
	defer rows.Close()
	var list []*domain.Payment
	for rows.Next() {
		p, err := scanPayment(rows)
		if err != nil {
			return nil, 0, err
		}
		list = append(list, p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// IsProcessed сообщает, обработано ли событие.
func (r *Repository) IsProcessed(ctx context.Context, eventID string) (bool, error) {
	var exists bool
	if err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM processed_events WHERE event_id = $1)`, eventID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check processed event: %w", err)
	}
	return exists, nil
}

// CreateOnce атомарно отмечает событие обработанным, сохраняет платёж и события.
func (r *Repository) CreateOnce(ctx context.Context, eventID, eventType string, p *domain.Payment, evts []events.Envelope) (bool, error) {
	created := false
	err := postgres.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		ok, err := markProcessed(ctx, tx, eventID, eventType)
		if err != nil || !ok {
			return err
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO payments (id, order_id, customer_id, amount, status, failure_reason, provider_ref, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (order_id) DO NOTHING`,
			p.ID, p.OrderID, p.CustomerID, p.Amount, string(p.Status), p.FailureReason, p.ProviderRef, p.CreatedAt, p.UpdatedAt)
		if err != nil {
			return fmt.Errorf("insert payment: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrPaymentExists // откат всей транзакции, включая отметку события
		}
		if err := outbox.Add(ctx, tx, evts...); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return created, nil
}

// UpdateOnce блокирует платёж заказа и применяет изменения не более одного
// раза для события eventID.
func (r *Repository) UpdateOnce(ctx context.Context, eventID, eventType, orderID string, fn service.MutateFunc) (bool, error) {
	applied := false
	err := postgres.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		ok, err := markProcessed(ctx, tx, eventID, eventType)
		if err != nil || !ok {
			return err
		}
		p, err := scanPayment(tx.QueryRow(ctx,
			`SELECT `+paymentColumns+` FROM payments WHERE order_id = $1 FOR UPDATE`, orderID))
		if err != nil {
			return err
		}
		evts, err := fn(p)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE payments SET status = $2, failure_reason = $3, provider_ref = $4, updated_at = $5
			WHERE id = $1`,
			p.ID, string(p.Status), p.FailureReason, p.ProviderRef, p.UpdatedAt); err != nil {
			return fmt.Errorf("update payment: %w", err)
		}
		if err := outbox.Add(ctx, tx, evts...); err != nil {
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

func scanPayment(row pgx.Row) (*domain.Payment, error) {
	var (
		p      domain.Payment
		status string
	)
	err := row.Scan(&p.ID, &p.OrderID, &p.CustomerID, &p.Amount, &status, &p.FailureReason,
		&p.ProviderRef, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if postgres.IsNoRows(err) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scan payment: %w", err)
	}
	p.Status = domain.Status(status)
	return &p, nil
}
