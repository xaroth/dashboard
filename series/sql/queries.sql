-- name: AddName :exec
INSERT INTO series_names (name) VALUES (?) ON CONFLICT (name) DO NOTHING;

-- name: GetName :one
SELECT id FROM series_names WHERE name = ?;

-- name: AddSample :exec
INSERT INTO series_raw (series, ts, value) VALUES (?, ?, ?)
ON CONFLICT (series, ts) DO UPDATE SET value = excluded.value;

-- name: ListSamples :many
SELECT ts, value FROM series_raw
WHERE series = @series AND ts >= @from AND ts < @to
ORDER BY ts;

-- name: SetBucket :exec
INSERT INTO series_rollup (series, res, ts, value, n) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (series, res, ts) DO UPDATE SET value = excluded.value, n = excluded.n;

-- name: ListBuckets :many
SELECT ts, value, n FROM series_rollup
WHERE series = @series AND res = @res AND ts >= @from AND ts < @to
ORDER BY ts;

-- name: DeleteSamples :exec
DELETE FROM series_raw WHERE series = @series AND ts < @before;

-- name: DeleteBuckets :exec
DELETE FROM series_rollup WHERE series = @series AND res = @res AND ts < @before;
