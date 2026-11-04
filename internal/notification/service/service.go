// Package service — слой бизнес-логики Notification Service.
//
// Сервис подписан на доменные события всех остальных сервисов (паттерн
// Publish/Subscribe): по каждому событию он определяет получателей и тексты
// (domain.Compose), сохраняет уведомления (история доступна по gRPC) и
// отправляет их в реальном времени через core NATS — API Gateway доставляет
// их подключённым клиентам по Server-Sent Events.
package service

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thaperfectcell/food-delivery/internal/notification/domain"
	"github.com/thaperfectcell/food-delivery/pkg/apperr"
	"github.com/thaperfectcell/food-delivery/pkg/auth"
	"github.com/thaperfectcell/food-delivery/pkg/events"
)

// Repository — порт хранилища уведомлений.
type Repository interface {
	// SaveOnce в одной транзакции отмечает событие обработанным и сохраняет
	// уведомления. false без ошибки — событие уже обработано ранее.
	SaveOnce(ctx context.Context, eventID, eventType string, list []domain.Notification) (bool, error)
	// List возвращает уведомления пользователя (новые первыми) и число непрочитанных.
	List(ctx context.Context, userID string, limit int, unreadOnly bool) ([]domain.Notification, int, error)
	// MarkRead отмечает прочитанными уведомления пользователя (ids или все).
	MarkRead(ctx context.Context, userID string, ids []string, all bool) (int, error)
}

// Pusher — порт доставки уведомления в реальном времени (core NATS).
type Pusher interface {
	Push(ctx context.Context, n domain.Notification) error
}

// Service реализует сценарии Notification Service.
type Service struct {
	repo   Repository
	pusher Pusher
	log    *slog.Logger
	now    func() time.Time
	newID  func() string
}

// New создаёт сервис.
func New(repo Repository, pusher Pusher, log *slog.Logger) *Service {
	return &Service{
		repo:   repo,
		pusher: pusher,
		log:    log,
		now:    func() time.Time { return time.Now().UTC() },
		newID:  uuid.NewString,
	}
}

// HandleEvent формирует, сохраняет и рассылает уведомления по событию.
// Повторная доставка события не создаёт дубликатов (идемпотентный потребитель).
// Push выполняется после фиксации транзакции и в режиме best effort: при сбое
// уведомление всё равно доступно в истории.
func (s *Service) HandleEvent(ctx context.Context, e events.Envelope) error {
	drafts, err := domain.Compose(e)
	if err != nil {
		return err
	}
	now := s.now()
	list := make([]domain.Notification, 0, len(drafts))
	for _, d := range drafts {
		if _, err := uuid.Parse(d.UserID); err != nil {
			s.log.WarnContext(ctx, "notification recipient skipped: invalid user id",
				slog.String("event_id", e.ID), slog.String("type", e.Type), slog.String("user_id", d.UserID))
			continue
		}
		orderID := d.OrderID
		if _, err := uuid.Parse(orderID); err != nil {
			orderID = ""
		}
		list = append(list, domain.Notification{
			ID:        s.newID(),
			UserID:    d.UserID,
			Type:      e.Type,
			Title:     d.Title,
			Body:      d.Body,
			OrderID:   orderID,
			CreatedAt: now,
		})
	}
	if len(list) == 0 {
		return nil // событие не требует уведомлений
	}

	saved, err := s.repo.SaveOnce(ctx, e.ID, e.Type, list)
	if err != nil {
		return err
	}
	if !saved {
		s.log.InfoContext(ctx, "duplicate event skipped", slog.String("event_id", e.ID), slog.String("type", e.Type))
		return nil
	}
	for _, n := range list {
		if err := s.pusher.Push(ctx, n); err != nil {
			s.log.WarnContext(ctx, "push failed, notification kept in history",
				slog.String("notification_id", n.ID), slog.String("user_id", n.UserID), slog.String("error", err.Error()))
			continue
		}
		s.log.InfoContext(ctx, "notification sent",
			slog.String("type", n.Type), slog.String("user_id", n.UserID), slog.String("title", n.Title))
	}
	return nil
}

// ListNotifications возвращает уведомления текущего пользователя.
func (s *Service) ListNotifications(ctx context.Context, actor auth.Identity, limit int, unreadOnly bool) ([]domain.Notification, int, error) {
	if err := requireUser(actor); err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = domain.DefaultListLimit
	}
	if limit > domain.MaxListLimit {
		limit = domain.MaxListLimit
	}
	return s.repo.List(ctx, actor.UserID, limit, unreadOnly)
}

// MarkRead отмечает прочитанными уведомления текущего пользователя: перечисленные
// в ids или все (all). Чужие уведомления не затрагиваются.
func (s *Service) MarkRead(ctx context.Context, actor auth.Identity, ids []string, all bool) (int, error) {
	if err := requireUser(actor); err != nil {
		return 0, err
	}
	if !all {
		if len(ids) == 0 {
			return 0, domain.ErrNothingToMark
		}
		if len(ids) > domain.MaxMarkReadIDs {
			return 0, domain.ErrTooManyIDs
		}
		clean := make([]string, 0, len(ids))
		for _, id := range ids {
			id = strings.TrimSpace(id)
			if _, err := uuid.Parse(id); err != nil {
				return 0, domain.ErrInvalidID
			}
			clean = append(clean, id)
		}
		ids = clean
	} else {
		ids = nil
	}
	return s.repo.MarkRead(ctx, actor.UserID, ids, all)
}

// requireUser проверяет, что вызывающий — аутентифицированный пользователь.
func requireUser(actor auth.Identity) error {
	if actor.IsZero() {
		return apperr.Unauthenticated("требуется вход в систему")
	}
	if _, err := uuid.Parse(actor.UserID); err != nil {
		return apperr.Forbidden("уведомления доступны только пользователям")
	}
	return nil
}
