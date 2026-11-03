-- Notification Service: схема базы данных notification

CREATE TABLE notifications (
    id         UUID PRIMARY KEY,
    user_id    UUID        NOT NULL, -- получатель (пользователь Auth Service)
    type       TEXT        NOT NULL, -- тип исходного события, например order.accepted
    title      TEXT        NOT NULL,
    body       TEXT        NOT NULL,
    order_id   UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    read_at    TIMESTAMPTZ
);

-- лента уведомлений пользователя (новые первыми)
CREATE INDEX idx_notifications_user   ON notifications (user_id, created_at DESC);
-- счётчик непрочитанных
CREATE INDEX idx_notifications_unread ON notifications (user_id) WHERE read_at IS NULL;

-- Idempotent Consumer
CREATE TABLE processed_events (
    event_id     UUID PRIMARY KEY,
    event_type   TEXT        NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
