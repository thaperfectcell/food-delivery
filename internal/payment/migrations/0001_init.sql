-- Payment Service: схема базы данных payment

CREATE TABLE payments (
    id             UUID PRIMARY KEY,
    order_id       UUID        NOT NULL,
    customer_id    UUID        NOT NULL,
    amount         BIGINT      NOT NULL CHECK (amount >= 0),
    status         TEXT        NOT NULL CHECK (status IN ('pending', 'succeeded', 'failed', 'refunded')),
    failure_reason TEXT        NOT NULL DEFAULT '',
    provider_ref   TEXT        NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- не более одного платежа на заказ
    CONSTRAINT uq_payments_order UNIQUE (order_id)
);

CREATE INDEX idx_payments_customer ON payments (customer_id, created_at DESC);
CREATE INDEX idx_payments_created  ON payments (created_at DESC);

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
