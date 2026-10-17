-- name: SaveSample :exec
INSERT INTO eveonline_status_samples (at, online, players) VALUES (?, ?, ?)
ON CONFLICT (at) DO UPDATE SET online = excluded.online, players = excluded.players;

-- name: ListSamples :many
SELECT * FROM eveonline_status_samples WHERE at >= ? ORDER BY at;
