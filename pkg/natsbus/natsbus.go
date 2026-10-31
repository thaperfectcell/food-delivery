// Package natsbus — адаптер к брокеру сообщений NATS (JetStream).
//
// Обеспечивает: подключение с автоматическим переподключением, идемпотентное
// создание потоков, публикацию с дедупликацией (Nats-Msg-Id) и потребление
// через durable pull-консьюмеры с явным подтверждением (at-least-once).
package natsbus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/thaperfectcell/food-delivery/pkg/events"
	"github.com/thaperfectcell/food-delivery/pkg/requestid"
)

// Bus — подключение к NATS и JetStream.
type Bus struct {
	NC  *nats.Conn
	JS  jetstream.JetStream
	log *slog.Logger
}

// Connect подключается к NATS. Если сервер ещё не запущен, клиент продолжит
// попытки в фоне (RetryOnFailedConnect), а EnsureStreams дождётся готовности.
func Connect(url, clientName string, log *slog.Logger) (*Bus, error) {
	log = log.With(slog.String("component", "nats"))
	nc, err := nats.Connect(url,
		nats.Name(clientName),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				log.Warn("nats disconnected", slog.String("error", err.Error()))
			}
		}),
		nats.ReconnectHandler(func(c *nats.Conn) {
			log.Info("nats reconnected", slog.String("url", c.ConnectedUrl()))
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to nats: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("init jetstream: %w", err)
	}
	return &Bus{NC: nc, JS: js, log: log}, nil
}

// EnsureStreams создаёт (или обновляет) потоки системы, повторяя попытки,
// пока NATS не станет доступен или не истечёт контекст.
func (b *Bus) EnsureStreams(ctx context.Context) error {
	for attempt := 1; ; attempt++ {
		err := b.ensureStreamsOnce(ctx)
		if err == nil {
			return nil
		}
		if attempt%5 == 1 {
			b.log.Warn("jetstream is not ready, retrying", slog.Int("attempt", attempt), slog.String("error", err.Error()))
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("ensure streams: %w", err)
		case <-time.After(time.Second):
		}
	}
}

func (b *Bus) ensureStreamsOnce(ctx context.Context) error {
	for _, s := range events.Streams {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := b.JS.CreateOrUpdateStream(cctx, jetstream.StreamConfig{
			Name:       s.Name,
			Subjects:   s.Subjects,
			Retention:  jetstream.LimitsPolicy,
			Storage:    jetstream.FileStorage,
			MaxAge:     7 * 24 * time.Hour,
			Duplicates: 2 * time.Minute,
		})
		cancel()
		if err != nil {
			return fmt.Errorf("stream %s: %w", s.Name, err)
		}
	}
	return nil
}

// Publish публикует сообщение в JetStream с идентификатором для дедупликации.
// Реализует outbox.Publisher.
func (b *Bus) Publish(ctx context.Context, subject, msgID string, data []byte) error {
	_, err := b.JS.Publish(ctx, subject, data, jetstream.WithMsgID(msgID))
	return err
}

// Handler обрабатывает доменное событие. Ошибка приводит к повторной доставке
// (с задержкой), если только это не ErrPermanent.
type Handler func(ctx context.Context, e events.Envelope) error

// ErrPermanent помечает ошибку, повторять обработку которой бессмысленно
// (например, некорректный формат сообщения) — сообщение будет отброшено.
var ErrPermanent = errors.New("permanent event handling error")

// ConsumerSpec — параметры durable-потребителя.
type ConsumerSpec struct {
	Stream   string   // имя потока JetStream
	Durable  string   // имя durable-потребителя (общее для всех экземпляров сервиса)
	Subjects []string // фильтр субъектов
}

// Consume создаёт (или обновляет) durable-потребителя и начинает обработку.
// Несколько экземпляров сервиса с одинаковым Durable делят поток сообщений
// между собой (конкурирующие потребители), что даёт горизонтальное масштабирование.
func (b *Bus) Consume(ctx context.Context, spec ConsumerSpec, h Handler) (stop func(), err error) {
	var cons jetstream.Consumer
	for attempt := 1; ; attempt++ {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		cons, err = b.JS.CreateOrUpdateConsumer(cctx, spec.Stream, jetstream.ConsumerConfig{
			Durable:        spec.Durable,
			FilterSubjects: spec.Subjects,
			AckPolicy:      jetstream.AckExplicitPolicy,
			DeliverPolicy:  jetstream.DeliverAllPolicy,
			AckWait:        30 * time.Second,
			MaxDeliver:     10,
			MaxAckPending:  256,
		})
		cancel()
		if err == nil {
			break
		}
		if attempt >= 30 || ctx.Err() != nil {
			return nil, fmt.Errorf("create consumer %s: %w", spec.Durable, err)
		}
		time.Sleep(time.Second)
	}

	log := b.log.With(slog.String("consumer", spec.Durable))
	cc, err := cons.Consume(func(msg jetstream.Msg) {
		env, perr := events.Parse(msg.Data())
		if perr != nil {
			log.Error("drop malformed message", slog.String("subject", msg.Subject()), slog.String("error", perr.Error()))
			_ = msg.Term()
			return
		}
		hctx, cancel := context.WithTimeout(requestid.With(context.Background(), env.TraceID), 25*time.Second)
		defer cancel()

		start := time.Now()
		if herr := h(hctx, env); herr != nil {
			if errors.Is(herr, ErrPermanent) {
				log.ErrorContext(hctx, "event rejected", slog.String("type", env.Type), slog.String("event_id", env.ID), slog.String("error", herr.Error()))
				_ = msg.Term()
				return
			}
			delivered := uint64(1)
			if md, err := msg.Metadata(); err == nil {
				delivered = md.NumDelivered
			}
			delay := backoff(delivered)
			log.WarnContext(hctx, "event handling failed, will retry",
				slog.String("type", env.Type), slog.String("event_id", env.ID),
				slog.Uint64("delivery", delivered), slog.Duration("retry_in", delay),
				slog.String("error", herr.Error()))
			_ = msg.NakWithDelay(delay)
			return
		}
		if err := msg.Ack(); err != nil {
			log.WarnContext(hctx, "ack failed", slog.String("error", err.Error()))
			return
		}
		log.InfoContext(hctx, "event handled",
			slog.String("type", env.Type), slog.String("event_id", env.ID),
			slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000))
	}, jetstream.PullMaxMessages(prefetch)) // небольшой буфер: сообщения равномерно делятся между экземплярами
	if err != nil {
		return nil, fmt.Errorf("consume %s: %w", spec.Durable, err)
	}
	log.Info("consumer started", slog.String("stream", spec.Stream), slog.Any("subjects", spec.Subjects))
	return cc.Stop, nil
}

// prefetch — сколько сообщений экземпляр забирает из потока заранее. Большой
// буфер ускоряет одиночный экземпляр, но при горизонтальном масштабировании
// первый запустившийся экземпляр «забирает» всю очередь, и остальные простаивают.
const prefetch = 8

// backoff — экспоненциальная задержка повторной доставки: 1s, 2s, 4s … 30s.
func backoff(delivered uint64) time.Duration {
	d := time.Second
	for i := uint64(1); i < delivered && d < 30*time.Second; i++ {
		d *= 2
	}
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

// Close корректно закрывает соединение (с дренажом подписок).
func (b *Bus) Close() {
	if b == nil || b.NC == nil {
		return
	}
	if err := b.NC.Drain(); err != nil {
		b.NC.Close()
	}
}
