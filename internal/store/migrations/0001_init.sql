-- +goose Up
-- Multi-user budget planner. Every user owns their plan (incomes, categories,
-- expenses), target currency and tracked-currency list. Rates are a global
-- USD-based cache refreshed by the server; per-user conversion is a cross
-- rate. Money and percents are decimal strings (TEXT) — never floats.
-- Session expiry is a Unix timestamp (seconds, UTC).

CREATE TABLE users (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    login           TEXT    NOT NULL UNIQUE,
    password_hash   TEXT    NOT NULL,
    is_admin        INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
    target_currency TEXT    NOT NULL DEFAULT 'USD'
);

CREATE TABLE sessions (
    token_hash TEXT    PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL
);
CREATE INDEX sessions_user_id ON sessions(user_id);

CREATE TABLE user_currencies (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code    TEXT    NOT NULL,
    PRIMARY KEY (user_id, code)
);

CREATE TABLE rates (
    code       TEXT PRIMARY KEY,
    rate_usd   TEXT NOT NULL,
    fetched_at TEXT NOT NULL
);

CREATE TABLE categories (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name     TEXT    NOT NULL,
    kind     TEXT    NOT NULL DEFAULT 'expense' CHECK (kind IN ('expense', 'saving')),
    color    INTEGER NOT NULL CHECK (color BETWEEN 1 AND 8),
    position INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX categories_user_id ON categories(user_id);

CREATE TABLE incomes (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name     TEXT    NOT NULL,
    amount   TEXT    NOT NULL DEFAULT '0',
    currency TEXT    NOT NULL,
    period   TEXT    NOT NULL DEFAULT 'monthly' CHECK (period IN ('monthly', 'annual')),
    position INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX incomes_user_id ON incomes(user_id);

CREATE TABLE expenses (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    category_id    INTEGER NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    name           TEXT    NOT NULL,
    amount         TEXT    NOT NULL DEFAULT '0',
    currency       TEXT    NOT NULL,
    period         TEXT    NOT NULL DEFAULT 'monthly' CHECK (period IN ('monthly', 'annual')),
    day            INTEGER,
    markup_percent TEXT    NOT NULL DEFAULT '0',
    position       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX expenses_category_id ON expenses(category_id);

-- +goose Down
DROP TABLE expenses;
DROP TABLE incomes;
DROP TABLE categories;
DROP TABLE rates;
DROP TABLE user_currencies;
DROP TABLE sessions;
DROP TABLE users;
