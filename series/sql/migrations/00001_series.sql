-- +goose Up
CREATE TABLE series_names (
    id   INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);

-- Samples as written; ts in Unix seconds keeps rows small.
CREATE TABLE series_raw (
    series INTEGER NOT NULL,
    ts     INTEGER NOT NULL,
    value  REAL NOT NULL,
    PRIMARY KEY (series, ts)
) WITHOUT ROWID;

-- Buckets of res seconds starting at ts; n is the number of raw samples.
CREATE TABLE series_rollup (
    series INTEGER NOT NULL,
    res    INTEGER NOT NULL,
    ts     INTEGER NOT NULL,
    value  REAL NOT NULL,
    n      INTEGER NOT NULL,
    PRIMARY KEY (series, res, ts)
) WITHOUT ROWID;

-- +goose Down
DROP TABLE series_rollup;
DROP TABLE series_raw;
DROP TABLE series_names;
