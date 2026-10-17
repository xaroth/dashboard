-- +goose Up
-- The imported build. A migration that changes the tables below deletes this
-- row, so the next update imports the current build again.
CREATE TABLE eveonline_sde_build (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    build_number INTEGER NOT NULL,
    release_date TIMESTAMP NOT NULL
);

-- The datasets the imported build has; the tables of others are empty.
CREATE TABLE eveonline_sde_datasets (
    name TEXT PRIMARY KEY
);

CREATE TABLE eveonline_sde_categories (
    category_id INTEGER PRIMARY KEY,
    name TEXT NOT NULL
);

CREATE TABLE eveonline_sde_groups (
    group_id INTEGER PRIMARY KEY,
    category_id INTEGER NOT NULL,
    name TEXT NOT NULL
);

CREATE TABLE eveonline_sde_types (
    type_id INTEGER PRIMARY KEY,
    group_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    published BOOLEAN NOT NULL
);

CREATE TABLE eveonline_sde_regions (
    region_id INTEGER PRIMARY KEY,
    name TEXT NOT NULL
);

CREATE TABLE eveonline_sde_constellations (
    constellation_id INTEGER PRIMARY KEY,
    region_id INTEGER NOT NULL,
    name TEXT NOT NULL
);

CREATE TABLE eveonline_sde_solar_systems (
    solar_system_id INTEGER PRIMARY KEY,
    constellation_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    security_status REAL NOT NULL
);
