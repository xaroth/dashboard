-- +goose Up
-- The OAuth error code of the rejected refresh. Only invalid_grant stops
-- refreshes; errors recorded before this column have none, so their
-- characters are tried again.
ALTER TABLE eveonline_esi_characters ADD COLUMN auth_error_code TEXT;

-- +goose Down
ALTER TABLE eveonline_esi_characters DROP COLUMN auth_error_code;
