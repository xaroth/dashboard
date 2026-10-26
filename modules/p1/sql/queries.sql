-- name: StartDay :exec
INSERT INTO p1_days (date, read_at, delivered_t1, delivered_t2, returned_t1, returned_t2)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (date) DO NOTHING;

-- name: ListDays :many
SELECT * FROM p1_days WHERE date >= ? ORDER BY date;

-- name: StartGasDay :exec
INSERT INTO p1_gas_days (date, read_at, gas)
VALUES (?, ?, ?)
ON CONFLICT (date) DO NOTHING;

-- name: ListGasDays :many
SELECT * FROM p1_gas_days WHERE date >= ? ORDER BY date;
