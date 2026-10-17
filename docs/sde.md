# EVE Online static data

`sde.Shared` (`modules/eveonline/sde`, shared value `eveonline_sde`) imports parts of the EVE Online [Static Data Export](https://developers.eveonline.com/docs/services/static-data/) (SDE) into typed SQLite tables and keeps them at the latest build. Modules read it through `sde.Shared.Get(ctx)`; the tables belong to `eveonline_sde`, so no other package queries them directly. Like every shared value, it is built and scheduled only when a registered module declares it.

## Updates

- Every hour `Update` reads the build number from `https://developers.eveonline.com/static-data/tranquility/latest.jsonl` (record `sde`). When it differs from the imported build, or the imported build lacks a declared dataset, it downloads `eve-online-static-data-<build>-jsonl.zip` (about 100 MB) to a temporary file and imports it.
- The import checks that the archive's `_sde.jsonl` has the expected build, then replaces every table in one transaction: readers see the previous build or the new one, never a mix. A failed import keeps the previous build. The transaction holds the only writer connection, so other modules' writes wait for it; keep imported datasets small.
- Status, backoff, Update now and `schedule` overrides work as for modules, under the name `eveonline_sde`. Data is stale after 6 hours without a successful check. One update may take 10 minutes.

## Datasets

Modules declare the datasets they need: `module.WithShared(sde.Shared, sde.Types, sde.SolarSystems)`. Only declared datasets are imported; the tables of the others are empty. A lookup on a dataset no module declared returns an error. An unknown dataset, or none at all, fails the build, which turns off every module using the SDE.

| Dataset | Lookup | SDE files |
| --- | --- | --- |
| `sde.Types` | `Store.Types(ctx, ids)`: `map[int64]sde.Type` | `categories`, `groups`, `types` |
| `sde.SolarSystems` | `Store.SolarSystems(ctx, ids)`: `map[int64]sde.SolarSystem` | `mapRegions`, `mapConstellations`, `mapSolarSystems` |

Lookups leave out IDs the imported build does not have, and return nothing before the first import. The admin UI shows the imported build and its release date under Shared.

| Type | Fields |
| --- | --- |
| `sde.Type` | `TypeID`, `Name`, `Published`, `GroupID`, `GroupName`, `CategoryID`, `CategoryName` |
| `sde.SolarSystem` | `SolarSystemID`, `Name`, `SecurityStatus`, `ConstellationID`, `ConstellationName`, `RegionID`, `RegionName` |

`SecurityStatus` is full precision. Display it rounded to one decimal, except that `0.0 < x < 0.05` shows as 0.1. High-sec is `>= 0.45`, low-sec `> 0.0`, null-sec `<= 0.0`.

## Tables

Only the fields the lookups return are imported. Names are English (`name.en` in the SDE).

| Table | Dataset | Columns (SDE field) |
| --- | --- | --- |
| `eveonline_sde_build` | | `id` (always 1), `build_number` (`buildNumber`), `release_date` (`releaseDate`) |
| `eveonline_sde_datasets` | | `name`: the datasets the imported build has |
| `eveonline_sde_categories` | `types` | `category_id` (`_key`), `name` |
| `eveonline_sde_groups` | `types` | `group_id` (`_key`), `category_id` (`categoryID`), `name` |
| `eveonline_sde_types` | `types` | `type_id` (`_key`), `group_id` (`groupID`), `name`, `published` (`published`) |
| `eveonline_sde_regions` | `solar_systems` | `region_id` (`_key`), `name` |
| `eveonline_sde_constellations` | `solar_systems` | `constellation_id` (`_key`), `region_id` (`regionID`), `name` |
| `eveonline_sde_solar_systems` | `solar_systems` | `solar_system_id` (`_key`), `constellation_id` (`constellationID`), `name`, `security_status` (`securityStatus`) |

Every column is `NOT NULL`. IDs are `INTEGER` primary keys, `published` is `BOOLEAN`, `security_status` is `REAL`, times are `TIMESTAMP`.

## Tests

`sde.NewFake(tb, opts...)` serves `latest.jsonl` and the archive of its build, for tests of modules using the SDE: route requests to `developers.eveonline.com` to it. `WithBuild`, `WithFile` and `WithoutFile` set the build and its JSON Lines files; `Set` changes them while serving, e.g. to publish a new build; `Downloads` counts archive downloads.

## Adding a dataset or field

1. Add the `Dataset` constant, or the field, and its tables or columns in a new migration. A migration that changes existing tables also deletes the `eveonline_sde_build` row, so the next update imports again; a new dataset is imported once a module declares it.
2. Add insert, delete and lookup queries to `sql/queries.sql`; run `go tool sqlc generate`.
3. Add an `importer` (clear and load) to `importers` in `update.go`, and a lookup method on `Store` that calls `check`.
4. Document the tables here.
