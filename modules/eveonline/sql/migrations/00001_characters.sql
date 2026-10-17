-- +goose Up
-- Characters logged in through EVE SSO, with their tokens.
CREATE TABLE eveonline_characters (
    character_id  INTEGER PRIMARY KEY,
    name          TEXT NOT NULL,
    -- Granted scopes, space-separated.
    scopes        TEXT NOT NULL,
    refresh_token TEXT NOT NULL,
    access_token  TEXT NOT NULL,
    expires_at    TIMESTAMP NOT NULL,
    -- Why the last refresh was rejected; cleared by logging in again.
    auth_error    TEXT
);

-- +goose Down
DROP TABLE eveonline_characters;
