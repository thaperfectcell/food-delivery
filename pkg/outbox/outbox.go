// Package outbox реализует паттерн Transactional Outbox.
//
// Проблема: сервису нужно атомарно изменить своё состояние в БД и опубликовать
// событие в брокер. Двухфазный коммит между PostgreSQL и NATS недоступен,
// поэтому событие сначала записывается в таблицу outbox в той же транзакции,
// что и бизнес-данные, а фоновый релей (Relay) публикует его в JetStream.
// Повторная публикация после сбоя безопасна: JetStream отбрасывает дубликаты
// по заголовку Nats-Msg-Id (= id события), потребители идемпотентны.
//
// Таблица (создаётся миграциями сервиса):
//
//	CREATE TABLE outbox (
//	    id           UUID PRIMARY KEY,
//	    subject      TEXT NOT NULL,
//	    payload      JSONB NOT NULL,
//	    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
//	    published_at TIMESTAMPTZ,
//	    attempts     INT NOT NULL DEFAULT 0,
//	    last_error   TEXT
//	);
package outbox

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/thaperfectcell/food-delivery/pkg/events"
)

// Add сохраняет события в outbox в рамках транзакции tx.
func Add(ctx context.Context, tx pgx.Tx, evts ...events.Envelope) error {
	for _, e := range evts {
		payload, err := e.Marshal()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO outbox (id, subject, payload, created_at) VALUES ($1, $2, $3, $4)`,
			e.ID, e.Type, payload, e.OccurredAt,
		); err != nil {
			return fmt.Errorf("insert outbox event %s: %w", e.Type, err)
		}
	}
	return nil
}

// Publisher публикует сообщение в брокер (реализуется natsbus.Bus).
type Publisher interface {
	Publish(ctx context.Context, subject, msgID string, data []byte) error
}

// Relay — фоновый процесс доставки событий из outbox в брокер.
type Relay struct {
	pool      *pgxpool.Pool
	pub       Publisher
	log       *slog.Logger
	interval  time.Duration
	batchSize int
	retention time.Duration
}

// NewRelay создаёт релей с параметрами по умолчанию: опрос каждые 200 мс,
// пачка до 100 событий, хранение опубликованных событий 24 часа.
func NewRelay(pool *pgxpool.Pool, pub Publisher, log *slog.Logger) *Relay {
	return &Relay{
		pool:      pool,
		pub:       pub,
		log:       log.With(slog.String("component", "outbox-relay")),
		interval:  200 * time.Millisecond,
		batchSize: 100,
		retention: 24 * time.Hour,
	}
}

// Run выполняет цикл публикации до отмены контекста.
func (r *Relay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	cleanup := time.NewTicker(time.Hour)
	defer cleanup.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-cleanup.C:
			if _, err := r.pool.Exec(ctx,
				`DELETE FROM outbox WHERE published_at IS NOT NULL AND published_at < $1`,
				time.Now().Add(-r.retention)); err != nil && ctx.Err() == nil {
				r.log.Warn("outbox cleanup failed", slog.String("error", err.Error()))
			}
		case <-ticker.C:
			// Публикуем пачками, пока очередь не опустеет.
			for {
				n, err := r.PublishBatch(ctx)
				if err != nil {
					if ctx.Err() == nil {
						r.log.Warn("outbox publish failed", slog.String("error", err.Error()))
					}
					break
				}
				if n < r.batchSize {
					break
				}
			}
		}
	}
}

// PublishBatch публикует одну пачку неопубликованных событий в порядке их
// создания. Строки блокируются FOR UPDATE SKIP LOCKED, поэтому несколько
// экземпляров сервиса не публикуют одно и то же событие одновременно.
// При ошибке публикации обработка пачки прерывается, чтобы сохранить порядок.
func (r *Relay) PublishBatch(ctx context.Context) (int, error) {
	published := 0
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, subject, payload
			FROM outbox
			WHERE published_at IS NULL
			ORDER BY created_at, id
			LIMIT $1
			FOR UPDATE SKIP LOCKED`, r.batchSize)
		if err != nil {
			return err
		}
		type item struct {
			id, subject string
			payload     []byte
		}
		var batch []item
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.id, &it.subject, &it.payload); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, it)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, it := range batch {
			pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			perr := r.pub.Publish(pubCtx, it.subject, it.id, it.payload)
			cancel()
			if perr != nil {
				if _, err := tx.Exec(ctx,
					`UPDATE outbox SET attempts = attempts + 1, last_error = $2 WHERE id = $1`,
					it.id, perr.Error()); err != nil {
					return err
				}
				return nil // фиксируем отметки уже опубликованных и счётчик попыток
			}
			if _, err := tx.Exec(ctx,
				`UPDATE outbox SET published_at = now(), attempts = attempts + 1, last_error = NULL WHERE id = $1`,
				it.id); err != nil {
				return err
			}
			published++
			r.log.Debug("event published", slog.String("subject", it.subject), slog.String("event_id", it.id))
		}
		return nil
	})
	return published, err
}
