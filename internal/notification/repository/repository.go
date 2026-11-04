// Package repository — слой доступа к данным Notification Service (PostgreSQL, pgx).
package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thaperfectcell/food-delivery/internal/notification/domain"
	"github.com/thaperfectcell/food-delivery/internal/notification/service"
	"github.com/thaperfectcell/food-delivery/pkg/postgres"
)

// Repository реализует service.Repository.
type Repository struct {
	pool *pgxpool.Pool
}

var _ service.Repository = (*Repository)(nil)

// New создаёт репозиторий.
func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// SaveOnce атомарно отмечает событие обработанным и сохраняет уведомления.
func (r *Repository) SaveOnce(ctx context.Context, eventID, eventType string, list []domain.Notification) (bool, error) {
	saved := false
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
		batch := &pgx.Batch{}
		for _, n := range list {
			batch.Queue(`
				INSERT INTO notifications (id, user_id, type, title, body, order_id, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				n.ID, n.UserID, n.Type, n.Title, n.Body, postgres.NullString(n.OrderID), n.CreatedAt)
		}
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return fmt.Errorf("insert notifications: %w", err)
		}
		saved = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return saved, nil
}

// List возвращает уведомления пользователя и количество непрочитанных.
func (r *Repository) List(ctx context.Context, userID string, limit int, unreadOnly bool) ([]domain.Notification, int, error) {
	var unread int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE user_id = $1 AND read_at IS NULL`, userID).Scan(&unread); err != nil {
		return nil, 0, fmt.Errorf("count unread notifications: %w", err)
	}
	query := `SELECT id, user_id, type, title, body, order_id, created_at, read_at
		FROM notifications WHERE user_id = $1`
	if unreadOnly {
		query += ` AND read_at IS NULL`
	}
	query += ` ORDER BY created_at DESC, id LIMIT $2`

	rows, err := r.pool.Query(ctx, query, userID, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()
	list := make([]domain.Notification, 0, limit)
	for rows.Next() {
		var (
			n       domain.Notification
			orderID *string
			readAt  *time.Time
		)
		if err := rows.Scan(&n.ID, &n.UserID, &n.Type, &n.Title, &n.Body, &orderID, &n.CreatedAt, &readAt); err != nil {
			return nil, 0, fmt.Errorf("scan notification: %w", err)
		}
		if orderID != nil {
			n.OrderID = *orderID
		}
		if readAt != nil {
			n.ReadAt = *readAt
		}
		list = append(list, n)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return list, unread, nil
}

// MarkRead отмечает прочитанными уведомления пользователя.
func (r *Repository) MarkRead(ctx context.Context, userID string, ids []string, all bool) (int, error) {
	var (
		sql  = `UPDATE notifications SET read_at = now() WHERE user_id = $1 AND read_at IS NULL`
		args = []any{userID}
	)
	if !all {
		sql += ` AND id = ANY($2::uuid[])`
		args = append(args, ids)
	}
	tag, err := r.pool.Exec(ctx, sql, args...)
	if err != nil {
		return 0, fmt.Errorf("mark notifications read: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
