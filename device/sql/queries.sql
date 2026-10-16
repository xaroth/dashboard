-- name: GetAPIKey :one
SELECT api_key FROM device_keys WHERE mac = ?;

-- name: CreateAPIKey :exec
-- Keeps an existing key, so concurrent setups agree on one.
INSERT INTO device_keys (mac, api_key) VALUES (?, ?)
ON CONFLICT (mac) DO NOTHING;
