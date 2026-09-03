-- +goose Up

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT uuidv7(),

    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT users_email_not_blank CHECK (length(trim(email)) > 0),
    CONSTRAINT users_email_normalized CHECK (email = lower(trim(email)))
);

-- +goose Down

DROP TABLE users;