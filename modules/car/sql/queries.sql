-- name: ListKeys :many
SELECT * FROM car_keys ORDER BY issued_at, client_id;

-- name: CountKeys :one
SELECT COUNT(*) FROM car_keys;

-- name: SaveKey :exec
INSERT INTO car_keys (client_id, client_secret, brand, issued_at, auth_error)
VALUES (?, ?, ?, ?, NULL)
ON CONFLICT (client_id) DO UPDATE SET
    client_secret = excluded.client_secret,
    brand         = excluded.brand,
    issued_at     = excluded.issued_at,
    auth_error    = NULL;

-- name: SaveKeyError :exec
UPDATE car_keys SET auth_error = ? WHERE client_id = ?;

-- name: DeleteKey :exec
DELETE FROM car_keys WHERE client_id = ?;

-- name: ListVehicles :many
SELECT * FROM car_vehicles ORDER BY vin;

-- name: SaveListing :exec
INSERT INTO car_vehicles (vin, client_id, nickname, name, sellname, type)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (vin) DO UPDATE SET
    client_id = excluded.client_id,
    nickname  = excluded.nickname,
    name      = excluded.name,
    sellname  = excluded.sellname,
    type      = excluded.type;

-- name: DeleteUnlisted :exec
DELETE FROM car_vehicles WHERE client_id = sqlc.arg(client_id) AND vin NOT IN (sqlc.slice(vins));

-- name: DeleteKeyVehicles :exec
DELETE FROM car_vehicles WHERE client_id = ?;

-- name: DeleteOrphanVehicles :exec
DELETE FROM car_vehicles WHERE client_id NOT IN (SELECT client_id FROM car_keys);

-- name: SaveShortName :exec
UPDATE car_vehicles SET short_name = ? WHERE vin = ?;

-- name: SaveActivity :exec
UPDATE car_vehicles
SET active_at = ?, latitude = ?, longitude = ?, odometer_km = ?
WHERE vin = ?;
