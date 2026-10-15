-- +goose Up
-- Latest update status per module, and the snapshot of modules whose data is one document.
CREATE TABLE module_state (
    module       TEXT PRIMARY KEY,
    attempted_at TIMESTAMP,
    fetched_at   TIMESTAMP,
    error        TEXT,
    data         TEXT
);

-- +goose Down
DROP TABLE module_state;
