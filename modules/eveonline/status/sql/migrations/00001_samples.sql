-- +goose Up
-- One row per update: the player count over time, offline included.
CREATE TABLE eveonline_status_samples (
    at      INTEGER PRIMARY KEY, -- Unix seconds
    online  BOOLEAN NOT NULL,
    players INTEGER NOT NULL
);

-- +goose Down
DROP TABLE eveonline_status_samples;
