-- +goose Up
-- Skill names from ESI /universe/names; they do not change.
CREATE TABLE eveonline_skills_names (
    skill_id INTEGER PRIMARY KEY,
    name     TEXT NOT NULL
);

-- +goose Down
DROP TABLE eveonline_skills_names;
