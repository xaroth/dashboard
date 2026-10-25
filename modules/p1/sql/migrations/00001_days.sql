-- +goose Up
-- The first reading of each local day; usage over a day is the difference
-- between consecutive rows.
CREATE TABLE p1_days (
    date         TEXT PRIMARY KEY, -- YYYY-MM-DD, the meter's local date
    read_at      TIMESTAMP NOT NULL,
    delivered_t1 REAL NOT NULL, -- kWh
    delivered_t2 REAL NOT NULL,
    returned_t1  REAL NOT NULL,
    returned_t2  REAL NOT NULL,
    gas          REAL -- m³, null without a gas meter
);

-- +goose Down
DROP TABLE p1_days;
