// Package realtime — источник push-уведомлений для SSE-потоков шлюза:
// подписка на субъекты core NATS notify.user.<id>, в которые Notification
// Service публикует уведомления пользователей.
package realtime

import (
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/thaperfectcell/food-delivery/pkg/events"
)

// bufferSize — сколько уведомлений может ожидать отправки клиенту; при
// переполнении (медленный клиент) лишние сообщения отбрасываются, чтобы не
// блокировать обработку NATS. Пропущенное клиент увидит в истории уведомлений.
const bufferSize = 64

// NATSSource реализует httpapi.NotificationSource поверх core NATS.
type NATSSource struct {
	nc *nats.Conn
}

// NewNATSSource создаёт источник уведомлений.
func NewNATSSource(nc *nats.Conn) *NATSSource { return &NATSSource{nc: nc} }

// ErrClosed — соединение с NATS закрыто.
var ErrClosed = errors.New("nats connection is closed")

// Subscribe подписывается на уведомления пользователя userID.
// Подписка во время переподключения к NATS допустима: клиент восстановит её
// автоматически после восстановления связи.
func (s *NATSSource) Subscribe(userID string) (<-chan []byte, func(), error) {
	if s.nc == nil || s.nc.IsClosed() {
		return nil, nil, ErrClosed
	}
	if _, err := uuid.Parse(userID); err != nil {
		return nil, nil, fmt.Errorf("invalid user id: %w", err)
	}
	out := make(chan []byte, bufferSize)
	done := make(chan struct{})
	sub, err := s.nc.Subscribe(events.UserNotifySubject(userID), func(m *nats.Msg) {
		data := append([]byte(nil), m.Data...)
		select {
		case <-done:
		case out <- data:
		default: // клиент не успевает читать — отбрасываем
		}
	})
	if err != nil {
		return nil, nil, fmt.Errorf("subscribe: %w", err)
	}
	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			_ = sub.Unsubscribe()
			close(done)
		})
	}
	return out, unsubscribe, nil
}
