-- +goose Up
-- The API key handed out by /api/setup, per device MAC.
CREATE TABLE device_keys (
    mac     TEXT PRIMARY KEY,
    api_key TEXT NOT NULL
);

-- +goose Down
DROP TABLE device_keys;
