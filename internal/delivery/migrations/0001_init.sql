-- Delivery Service: схема базы данных delivery

CREATE TABLE couriers (
    id                   UUID PRIMARY KEY, -- совпадает с идентификатором пользователя (Auth Service)
    name                 TEXT        NOT NULL,
    status               TEXT        NOT NULL DEFAULT 'offline'
                         CHECK (status IN ('offline', 'available', 'busy')),
    completed_deliveries INT         NOT NULL DEFAULT 0 CHECK (completed_deliveries >= 0),
    status_changed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- поиск свободного курьера, дольше всех ожидающего заказа
CREATE INDEX idx_couriers_available ON couriers (status_changed_at) WHERE status = 'available';

CREATE TABLE deliveries (
    id                 UUID PRIMARY KEY,
    order_id           UUID        NOT NULL,
    customer_id        UUID        NOT NULL,
    courier_id         UUID REFERENCES couriers (id),
    status             TEXT        NOT NULL
                       CHECK (status IN ('pending', 'assigned', 'picked_up', 'delivered', 'cancelled')),
    restaurant_name    TEXT        NOT NULL DEFAULT '',
    restaurant_address TEXT        NOT NULL DEFAULT '',
    delivery_address   TEXT        NOT NULL DEFAULT '',
    order_total        BIGINT      NOT NULL DEFAULT 0,
    order_ready        BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    assigned_at        TIMESTAMPTZ,
    picked_up_at       TIMESTAMPTZ,
    delivered_at       TIMESTAMPTZ,
    cancelled_at       TIMESTAMPTZ,
    -- одна доставка на заказ
    CONSTRAINT uq_deliveries_order UNIQUE (order_id)
);

CREATE INDEX idx_deliveries_courier ON deliveries (courier_id, created_at DESC);
-- очередь ожидающих назначения доставок (FIFO)
CREATE INDEX idx_deliveries_pending ON deliveries (created_at) WHERE status = 'pending';

-- Transactional Outbox
CREATE TABLE outbox (
    id           UUID PRIMARY KEY,
    subject      TEXT        NOT NULL,
    payload      JSONB       NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    attempts     INT         NOT NULL DEFAULT 0,
    last_error   TEXT
);

CREATE INDEX idx_outbox_unpublished ON outbox (created_at) WHERE published_at IS NULL;

-- Idempotent Consumer
CREATE TABLE processed_events (
    event_id     UUID PRIMARY KEY,
    event_type   TEXT        NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
