-- +goose Up
-- Pleos My Vehicle API keys, entered on the admin page. A key covers the
-- vehicles of one brand account (Bluelink, Kia Connect, Genesis Connect).
CREATE TABLE car_keys (
    client_id     TEXT PRIMARY KEY,
    client_secret TEXT NOT NULL,
    -- hyundai, kia or genesis: the Brand header of every call.
    brand         TEXT NOT NULL,
    -- When the key was issued in Pleos Playground; it expires 90 days later.
    issued_at     TIMESTAMP NOT NULL,
    -- Why Pleos last rejected the key; cleared by saving it again.
    auth_error    TEXT
);

-- Vehicles listed by the keys, and what the last poll saw of each, to
-- decide how soon to poll again.
CREATE TABLE car_vehicles (
    vin         TEXT PRIMARY KEY,
    -- The key that lists it.
    client_id   TEXT NOT NULL,
    nickname    TEXT NOT NULL,
    -- Model code, e.g. SX2HV.
    name        TEXT NOT NULL,
    -- Sales model name, e.g. KONA Electric.
    sellname    TEXT NOT NULL,
    -- GN, EV, HV, PE, FV or UNKNOWN.
    type        TEXT NOT NULL,
    -- Set on the admin page; pages select the vehicle by it or by VIN.
    short_name  TEXT UNIQUE,
    -- Last poll that found the car on, charging or moved.
    active_at   TIMESTAMP,
    -- Last known position and odometer, kept when a poll has none.
    latitude    REAL,
    longitude   REAL,
    odometer_km REAL
);

-- +goose Down
DROP TABLE car_vehicles;
DROP TABLE car_keys;
