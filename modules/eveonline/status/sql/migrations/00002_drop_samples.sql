-- +goose Up
-- The player count moved to the players series.
DROP TABLE eveonline_status_samples;

-- +goose Down
CREATE TABLE eveonline_status_samples (
    at      INTEGER PRIMARY KEY, -- Unix seconds
    online  BOOLEAN NOT NULL,
    players INTEGER NOT NULL
);
