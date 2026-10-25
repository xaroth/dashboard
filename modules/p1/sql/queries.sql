-- name: StartDay :exec
INSERT INTO p1_days (date, read_at, delivered_t1, delivered_t2, returned_t1, returned_t2, gas)
VALUES (?, ?, ?, ?, ?, ?, ?)
-- Gas comes in minutes after a dongle restart: fill it in when the day's
-- first reading had none.
ON CONFLICT (date) DO UPDATE SET gas = excluded.gas
WHERE p1_days.gas IS NULL AND excluded.gas IS NOT NULL;

-- name: ListDays :many
SELECT * FROM p1_days WHERE date >= ? ORDER BY date;
