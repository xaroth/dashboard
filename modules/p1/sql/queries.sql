-- name: StartDay :exec
INSERT INTO p1_days (date, read_at, delivered_t1, delivered_t2, returned_t1, returned_t2, gas)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (date) DO NOTHING;

-- name: ListDays :many
SELECT * FROM p1_days WHERE date >= ? ORDER BY date;
