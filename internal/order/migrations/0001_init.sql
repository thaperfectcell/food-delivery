-- Order Service: схема базы данных orders

CREATE TABLE orders (
    id                  UUID PRIMARY KEY,
    customer_id         UUID        NOT NULL,
    restaurant_id       UUID        NOT NULL,
    restaurant_owner_id UUID        NOT NULL,
    restaurant_name     TEXT        NOT NULL,
    restaurant_address  TEXT        NOT NULL,
    status              TEXT        NOT NULL CHECK (status IN
                        ('created', 'paid', 'accepted', 'ready', 'in_delivery', 'delivered', 'cancelled')),
    items_total         BIGINT      NOT NULL CHECK (items_total >= 0),
    delivery_fee        BIGINT      NOT NULL CHECK (delivery_fee >= 0),
    total               BIGINT      NOT NULL CHECK (total >= 0),
    delivery_address    TEXT        NOT NULL,
    comment             TEXT        NOT NULL DEFAULT '',
    courier_id          UUID,
    courier_name        TEXT        NOT NULL DEFAULT '',
    cancel_reason       TEXT        NOT NULL DEFAULT '',
    idempotency_key     TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_orders_idempotency UNIQUE (customer_id, idempotency_key)
);

CREATE INDEX idx_orders_customer ON orders (customer_id, created_at DESC);
CREATE INDEX idx_orders_owner    ON orders (restaurant_owner_id, created_at DESC);
CREATE INDEX idx_orders_status   ON orders (status);

CREATE TABLE order_items (
    order_id     UUID   NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    position     INT    NOT NULL,
    menu_item_id UUID   NOT NULL,
    name         TEXT   NOT NULL,
    price        BIGINT NOT NULL CHECK (price > 0),
    quantity     INT    NOT NULL CHECK (quantity > 0),
    PRIMARY KEY (order_id, position)
);

CREATE TABLE order_status_history (
    id         BIGSERIAL PRIMARY KEY,
    order_id   UUID        NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    status     TEXT        NOT NULL,
    reason     TEXT        NOT NULL DEFAULT '',
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_order_status_history_order ON order_status_history (order_id, id);

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
