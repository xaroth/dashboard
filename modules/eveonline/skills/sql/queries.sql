-- name: SaveName :exec
INSERT INTO eveonline_skills_names (skill_id, name) VALUES (?, ?)
ON CONFLICT (skill_id) DO UPDATE SET name = excluded.name;

-- name: ListNames :many
SELECT * FROM eveonline_skills_names;
