-- name: GetLocation :one
SELECT * FROM weather_locations WHERE query = ?;

-- name: SaveLocation :exec
INSERT INTO weather_locations (query, name, country, latitude, longitude, timezone, resolved_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (query) DO UPDATE SET
    name        = excluded.name,
    country     = excluded.country,
    latitude    = excluded.latitude,
    longitude   = excluded.longitude,
    timezone    = excluded.timezone,
    resolved_at = excluded.resolved_at;
