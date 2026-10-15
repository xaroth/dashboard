-- name: GetState :one
SELECT * FROM module_state WHERE module = ?;

-- name: ListStates :many
SELECT * FROM module_state ORDER BY module;

-- name: RecordSuccess :exec
INSERT INTO module_state (module, attempted_at, fetched_at, error, data)
VALUES (@module, @at, @at, NULL, @data)
ON CONFLICT (module) DO UPDATE SET
    attempted_at = excluded.attempted_at,
    fetched_at   = excluded.fetched_at,
    error        = NULL,
    data         = COALESCE(excluded.data, module_state.data);

-- name: RecordFailure :exec
INSERT INTO module_state (module, attempted_at, error)
VALUES (@module, @at, @error)
ON CONFLICT (module) DO UPDATE SET
    attempted_at = excluded.attempted_at,
    error        = excluded.error;
