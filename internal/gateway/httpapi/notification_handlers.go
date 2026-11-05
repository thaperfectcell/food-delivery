package httpapi

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	notificationv1 "github.com/thaperfectcell/food-delivery/gen/notification/v1"
	"github.com/thaperfectcell/food-delivery/internal/gateway/httpx"
	"github.com/thaperfectcell/food-delivery/internal/gateway/middleware"
)

// listNotifications — GET /notifications?unread=true&limit=N.
func (a *API) listNotifications(w http.ResponseWriter, r *http.Request) {
	limit, _, ok := paging(w, r)
	if !ok {
		return
	}
	ctx, cancel := a.call(r)
	defer cancel()
	resp, err := a.c.Notification.ListNotifications(ctx, &notificationv1.ListNotificationsRequest{
		Limit:      limit,
		UnreadOnly: queryBool(r, "unread"),
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	items := make([]notificationDTO, 0, len(resp.GetNotifications()))
	for _, n := range resp.GetNotifications() {
		items = append(items, toNotification(n))
	}
	httpx.WriteJSON(w, http.StatusOK, notificationListDTO{
		Items:  items,
		Total:  len(items),
		Unread: resp.GetUnreadCount(),
	})
}

type markReadRequest struct {
	IDs []string `json:"ids"`
	All bool     `json:"all"`
}

// markNotificationsRead — POST /notifications/read.
func (a *API) markNotificationsRead(w http.ResponseWriter, r *http.Request) {
	var in markReadRequest
	if !decodeJSON(w, r, &in, false) {
		return
	}
	if !in.All && len(in.IDs) == 0 {
		badRequest(w, r, "укажите ids или all=true")
		return
	}
	if len(in.IDs) > 500 {
		badRequest(w, r, "слишком много идентификаторов")
		return
	}
	ctx, cancel := a.call(r)
	defer cancel()
	resp, err := a.c.Notification.MarkRead(ctx, &notificationv1.MarkReadRequest{Ids: in.IDs, All: in.All})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]int32{"updated": resp.GetUpdated()})
}

// notificationStream — GET /notifications/stream: доставка уведомлений в
// реальном времени по протоколу Server-Sent Events.
//
// Notification Service публикует уведомление в core NATS (субъект
// notify.user.<id>), шлюз держит подписку на время SSE-соединения и
// пересылает сообщения браузеру. Браузерный EventSource не умеет задавать
// заголовки, поэтому токен передаётся параметром access_token.
func (a *API) notificationStream(w http.ResponseWriter, r *http.Request) {
	id, ok := middleware.Identity(r)
	if token := strings.TrimSpace(r.URL.Query().Get("access_token")); token != "" {
		parsed, err := a.tokens.Parse(token)
		if err != nil {
			httpx.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "недействительный или просроченный токен")
			return
		}
		id, ok = parsed, true
	}
	if !ok {
		httpx.WriteError(w, r, http.StatusUnauthorized, "unauthenticated", "требуется вход в систему")
		return
	}
	if a.notifier == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "unavailable", "уведомления в реальном времени временно недоступны")
		return
	}
	msgs, unsubscribe, err := a.notifier.Subscribe(id.UserID)
	if err != nil {
		a.log.WarnContext(r.Context(), "notification subscribe failed", slog.String("error", err.Error()))
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "unavailable", "уведомления в реальном времени временно недоступны")
		return
	}
	defer unsubscribe()

	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // поток живёт долго — снимаем таймаут записи сервера
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// retry — пауза перед автоматическим переподключением EventSource.
	if _, err := fmt.Fprintf(w, "retry: 3000\nevent: ready\ndata: {\"user_id\":%q}\n\n", id.UserID); err != nil {
		return
	}
	if err := rc.Flush(); err != nil {
		return
	}
	a.log.InfoContext(r.Context(), "sse stream opened", slog.String("user_id", id.UserID))
	defer a.log.InfoContext(r.Context(), "sse stream closed", slog.String("user_id", id.UserID))

	ping := time.NewTicker(a.ping)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-a.shutdown:
			return
		case msg, open := <-msgs:
			if !open {
				return
			}
			if err := writeSSE(w, "notification", msg); err != nil {
				return
			}
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}

// writeSSE записывает событие; многострочные данные разбиваются на строки data:.
func writeSSE(w http.ResponseWriter, event string, data []byte) error {
	var b bytes.Buffer
	b.WriteString("event: ")
	b.WriteString(event)
	b.WriteByte('\n')
	for _, line := range bytes.Split(bytes.TrimRight(data, "\r\n"), []byte("\n")) {
		b.WriteString("data: ")
		b.Write(bytes.TrimRight(line, "\r"))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	_, err := w.Write(b.Bytes())
	return err
}
