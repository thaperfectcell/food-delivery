-- Restaurant Service: схема базы данных restaurant

CREATE TABLE restaurants (
    id          UUID PRIMARY KEY,
    owner_id    UUID        NOT NULL,
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    address     TEXT        NOT NULL,
    cuisine     TEXT        NOT NULL DEFAULT '',
    is_active   BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_restaurants_owner  ON restaurants (owner_id);
CREATE INDEX idx_restaurants_active ON restaurants (is_active, name);

CREATE TABLE menu_items (
    id            UUID PRIMARY KEY,
    restaurant_id UUID        NOT NULL REFERENCES restaurants (id) ON DELETE CASCADE,
    name          TEXT        NOT NULL,
    description   TEXT        NOT NULL DEFAULT '',
    category      TEXT        NOT NULL DEFAULT '',
    price         BIGINT      NOT NULL CHECK (price > 0),
    is_available  BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_menu_items_restaurant ON menu_items (restaurant_id, category, name);
