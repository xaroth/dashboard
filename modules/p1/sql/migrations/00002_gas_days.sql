-- +goose Up
-- Gas days start at the gas meter's time, not the electricity meter's: a
-- DSMR 4 gas meter reports hourly, so at midnight its reading is from the
-- day before.
CREATE TABLE p1_gas_days (
    date    TEXT PRIMARY KEY, -- YYYY-MM-DD, the gas meter's local date
    read_at TIMESTAMP NOT NULL, -- the gas meter's time
    gas     REAL NOT NULL -- m³
);
INSERT INTO p1_gas_days (date, read_at, gas)
SELECT date, read_at, gas FROM p1_days WHERE gas IS NOT NULL;
ALTER TABLE p1_days DROP COLUMN gas;

-- +goose Down
ALTER TABLE p1_days ADD COLUMN gas REAL;
UPDATE p1_days SET gas = (SELECT gas FROM p1_gas_days WHERE p1_gas_days.date = p1_days.date);
DROP TABLE p1_gas_days;
