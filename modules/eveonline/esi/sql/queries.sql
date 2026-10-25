-- name: ListCharacters :many
SELECT * FROM eveonline_esi_characters ORDER BY name;

-- name: GetCharacter :one
SELECT * FROM eveonline_esi_characters WHERE character_id = ?;

-- name: SaveLogin :exec
INSERT INTO eveonline_esi_characters (character_id, name, scopes, refresh_token, access_token, expires_at, auth_error)
VALUES (?, ?, ?, ?, ?, ?, NULL)
ON CONFLICT (character_id) DO UPDATE SET
    name          = excluded.name,
    scopes        = excluded.scopes,
    refresh_token = excluded.refresh_token,
    access_token  = excluded.access_token,
    expires_at    = excluded.expires_at,
    auth_error    = NULL;

-- name: SaveRefresh :execrows
UPDATE eveonline_esi_characters
SET refresh_token = sqlc.arg(refresh_token), access_token = sqlc.arg(access_token),
    expires_at = sqlc.arg(expires_at), auth_error = NULL
WHERE character_id = sqlc.arg(character_id) AND refresh_token = sqlc.arg(old_refresh_token);

-- name: SaveAuthError :exec
UPDATE eveonline_esi_characters SET auth_error = ? WHERE character_id = ?;
