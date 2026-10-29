-- Auth Service: схема базы данных auth

CREATE TABLE users (
    id            UUID PRIMARY KEY,
    email         TEXT        NOT NULL,
    password_hash TEXT        NOT NULL,
    name          TEXT        NOT NULL,
    phone         TEXT        NOT NULL DEFAULT '',
    role          TEXT        NOT NULL CHECK (role IN ('customer', 'restaurant', 'courier', 'admin')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_users_email UNIQUE (email),
    CONSTRAINT chk_users_email_lower CHECK (email = lower(email))
);

CREATE INDEX idx_users_role ON users (role, created_at);
