-- +goose Up
-- Geocoding results per configured location string, so a lookup happens once.
CREATE TABLE weather_locations (
    query       TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    country     TEXT NOT NULL,
    latitude    REAL NOT NULL,
    longitude   REAL NOT NULL,
    timezone    TEXT NOT NULL,
    resolved_at TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE weather_locations;
