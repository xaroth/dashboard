-- name: GetBuild :one
SELECT build_number, release_date FROM eveonline_sde_build WHERE id = 1;

-- name: SetBuild :exec
INSERT INTO eveonline_sde_build (id, build_number, release_date) VALUES (1, ?, ?)
ON CONFLICT (id) DO UPDATE SET build_number = excluded.build_number, release_date = excluded.release_date;

-- name: ListDatasets :many
SELECT name FROM eveonline_sde_datasets ORDER BY name;

-- name: DeleteDatasets :exec
DELETE FROM eveonline_sde_datasets;

-- name: InsertDataset :exec
INSERT INTO eveonline_sde_datasets (name) VALUES (?);

-- name: DeleteCategories :exec
DELETE FROM eveonline_sde_categories;

-- name: DeleteGroups :exec
DELETE FROM eveonline_sde_groups;

-- name: DeleteTypes :exec
DELETE FROM eveonline_sde_types;

-- name: DeleteRegions :exec
DELETE FROM eveonline_sde_regions;

-- name: DeleteConstellations :exec
DELETE FROM eveonline_sde_constellations;

-- name: DeleteSolarSystems :exec
DELETE FROM eveonline_sde_solar_systems;

-- name: InsertCategory :exec
INSERT INTO eveonline_sde_categories (category_id, name) VALUES (?, ?);

-- name: InsertGroup :exec
INSERT INTO eveonline_sde_groups (group_id, category_id, name) VALUES (?, ?, ?);

-- name: InsertType :exec
INSERT INTO eveonline_sde_types (type_id, group_id, name, published) VALUES (?, ?, ?, ?);

-- name: InsertRegion :exec
INSERT INTO eveonline_sde_regions (region_id, name) VALUES (?, ?);

-- name: InsertConstellation :exec
INSERT INTO eveonline_sde_constellations (constellation_id, region_id, name) VALUES (?, ?, ?);

-- name: InsertSolarSystem :exec
INSERT INTO eveonline_sde_solar_systems (solar_system_id, constellation_id, name, security_status) VALUES (?, ?, ?, ?);

-- name: ListTypes :many
SELECT t.type_id, t.name, t.published, g.group_id, g.name AS group_name, c.category_id, c.name AS category_name
FROM eveonline_sde_types t
JOIN eveonline_sde_groups g ON g.group_id = t.group_id
JOIN eveonline_sde_categories c ON c.category_id = g.category_id
WHERE t.type_id IN (sqlc.slice('ids'));

-- name: ListSolarSystems :many
SELECT s.solar_system_id, s.name, s.security_status,
    c.constellation_id, c.name AS constellation_name, r.region_id, r.name AS region_name
FROM eveonline_sde_solar_systems s
JOIN eveonline_sde_constellations c ON c.constellation_id = s.constellation_id
JOIN eveonline_sde_regions r ON r.region_id = c.region_id
WHERE s.solar_system_id IN (sqlc.slice('ids'));
