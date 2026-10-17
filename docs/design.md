# Design: dashboard service

This document describes the service as built. Planned parts (more modules, time series, device health, more components, page tests, deployment) are specified in [plan.md](plan.md). Background and data source research: [research.md](research.md).

## Overview

One Go service, `dashboard`, serves two Seeed reTerminal e-ink units (E1001, E1003) running TRMNL firmware in BYOS mode. The service owns I/O, storage, scheduling, rendering and the device protocol. Data sources are modules: packages under `modules/`, listed in `modules.All` and configured in YAML. They are global to the system (not tied to a device).

```
             +------------------------- dashboard --------------------------+
             |                                                              |
 Open-Meteo -+--> modules/weather --> storage <-- queries --> render -------+--> device API <-- E1001
             |          ^            (SQLite)        |    (Bun: TSX+Takumi)  |               <-- E1003
             |      scheduler                        +--> admin UI: status, previews, simulator
             +--------------------------------------------------------------+
```

## Code layout

```
cmd/dashboard/             entrypoint and the only wiring point: config loading and
                           validation, storage, modules, renderer, listeners, admin UI
                           (status, preview, device simulator)
cmd/gentypes/              generates pages/generated from the module query types
storage/                   SQLite: connections, per-owner migrations
module/                    registration API, module contract, scheduler, query dispatch,
                           module state
module/sql/                module state migrations and queries
device/                    TRMNL protocol, API keys, page selection, screen schedule,
                           in-memory image store
device/sql/                API key migrations and queries
render/                    page pipeline, renderer process, gray reduction, PNG
modules/modules.go         modules.All: the list of every module
modules/<name>/            one module per data source
modules/<name>/sql/        the module's migrations and queries
modules/<group>/<name>/    a group's shared values and modules, each named
                           <group>_<name>: eveonline/esi (ESI client and characters),
                           eveonline/sde (static data), eveonline/status (a module)
module/moduletest/         helpers for module tests
renderer/                  Bun process: bundles pages, renders with takumi-js
pages/layouts/<layout>/    TSX pages and their CSS modules, one folder per layout
pages/components/, lib/    shared by all layouts
pages/generated/           TypeScript types generated from Go query results
tools/                     standalone scripts (dtu_read.py: read-only Modbus check)
```

Packages are one level deep and named for what they do. They do not import each other sideways; `cmd/dashboard` wires them together:

```
cmd/dashboard  -> storage, module, modules, device, render
cmd/gentypes   -> module, modules
modules        -> module, modules/<name>
modules/<name> -> module
modules/<group>/<name> -> module, the group's shared values
device         -> render
```

`module` and `device` do not import `storage`: `module.Load` and `device.Load` take their connections and a `Migrator`, which `*storage.DB` satisfies.

## Modules

| Module | Source | Data |
| --- | --- | --- |
| `eveonline_skills` | ESI `/characters/{id}/skillqueue`, `/characters/{id}/skills` (logged-in characters); SDE types for skill names | snapshot (each character's queue, trained skills and skill points) in module state |
| `eveonline_status` | ESI `/status` (EVE Online, Tranquility) | snapshot (online, players, server version, start time) in module state; one sample per update in `eveonline_status_samples` |
| `weather` | Open-Meteo forecast and geocoding APIs | snapshot (current, hourly, daily) in module state; geocoded location cached in `weather_locations` |

Planned modules are listed in [plan.md](plan.md#modules).

### Weather

Config keys under `modules.weather`:

| Key | Default | Meaning |
| --- | --- | --- |
| `location` | `Uithoorn, The Netherlands` | Place name, optionally with its country after a comma. Empty disables the module. |
| `forecast_days` | `7` | Days fetched, 1 to 16. Out of range fails `Initialize`. |
| `model` | empty | Open-Meteo weather model, e.g. `knmi_seamless` for KNMI HARMONIE. Empty lets Open-Meteo pick per location. |
| `geocoding_url` | `https://geocoding-api.open-meteo.com/v1/search` | Geocoding endpoint |
| `forecast_url` | `https://api.open-meteo.com/v1/forecast` | Forecast endpoint |

`Update` runs every 15 minutes; data is stale after 2 hours. The location is geocoded on the first update and cached per `location` string, so a changed value is looked up again. Query `weather.forecast` takes `hours` (hourly entries from the current hour, default 24) and `days` (daily entries from today, default 3).

### Registration

A module exports its registration, built with `module.Define`. `modules.All` lists every module explicitly, and `main` passes that list to `module.Load`; there is no global registry.

```go
package weather

import "github.com/xaroth/dashboard/module"

type config struct {
	Location string `yaml:"location"`
	Model    string `yaml:"model"`
}

func defaultConfig() *config {
	return &config{Location: "Uithoorn, The Netherlands"}
}

type weather struct {
	cfg *config
}

var Module = module.Define[weather, config](
	"weather",
	defaultConfig,
	module.WithUpdateInterval(15*time.Minute),
	module.WithStaleAfter(2*time.Hour),
	module.WithMigrations(migrations),
	module.WithQuery("forecast", (*weather).forecast),
)

func (m *weather) Initialize(ctx context.Context, cfg *config) error {
	m.cfg = cfg
	return nil
}

func (m *weather) Enabled(ctx context.Context) bool {
	return m.cfg.Location != ""
}

func (m *weather) Update(ctx context.Context) error {
	// geocode Location (cached in weather_locations), fetch the forecast,
	// then store it as the module's snapshot.
	return module.SaveSnapshot(ctx, forecast)
}
```

`Define` is generic over the module and config types. A third, inferred type parameter constrains `*M` to the module interface, so a missing method is a compile error:

```go
func Define[M any, C any, PM interface {
	*M
	Module[C]
}](name string, defaults func() *C, opts ...Option) Registration
```

```go
package modules

func All() []module.Registration {
	return []module.Registration{weather.Module}
}
```

Adding a module adds it to that list.

### Contract

Deliberately small; capabilities are added when a module needs them.

| Method | Purpose |
| --- | --- |
| `Initialize(ctx, *C) error` | Receive config, set up clients. The module's migrations have run by then. No network I/O: it runs for every registered module, configured or not. |
| `Enabled(ctx) bool` | Whether the module's schedule runs. Decided by the module, typically from its config (e.g. `weather` is enabled when `location` is set). |
| `Update(ctx) error` | Called by the scheduler: fetch from the source, write to storage. |

A module whose type also implements `http.Handler` gets an admin page: it is served on the admin listener under `/<name>/` (the prefix stripped), with the module's context on the request (as in `Update`: the writer, its shared values), and linked from the admin index under Accounts. Requests run concurrently with updates, so state they share goes through the database or is synchronized. A module whose `Initialize` failed is not served. A module or shared value page named after one of the admin UI's own path prefixes (`modules`, `simulate`, `images`, `preview`) stops startup. This is how settings like an API key are entered.

Options: `WithUpdateInterval` (default 15 minutes), `WithStaleAfter` (default 1 hour), `WithQuery` (see [Queries](#queries)), `WithMigrations` (see [SQL files](#sql-files)), `WithShared` (see [Shared values](#shared-values)).

Update and queries are split so a slow or failing source never blocks a screen: pages render from the last stored data.

### Configuration and lifecycle

- Every registered module is initialized with its defaults, overlaid with its `modules.<name>` section if present. Unknown keys, decode errors and a section for an unknown module stop startup: they are operator errors.
- The scheduler asks `Enabled` before each update. A disabled module skips the update and shows as disabled (not failed) in the admin UI; its queries still serve stored data, marked stale once older than `stale_after`. Because it is asked every time, a module can enable itself later, e.g. once a token appears in its table.
- Timing overrides live under a reserved `schedule` key that the service strips before decoding:

```yaml
modules:
  weather:
    schedule:
      update_interval: 30m
      stale_after: 3h
```

- A source being offline never stops the service: network work happens in `Update`, whose errors mark the module failed and are retried with backoff. Each update has a 2-minute timeout. If `Initialize` returns an error, the module is marked failed and stays off until restart. A panic in `Initialize`, `Enabled`, `Update` or a query is recovered and handled as an error from that call.
- Start: each schedule starts after a random delay of up to 30 s (or the interval, if shorter).
- Retries: exponential backoff from 30 s, doubling to at most 6 hours, not capped at the interval. A module returns `module.ErrAuth` (wrapped) for rejected credentials; that stops retries until a manual update (Update now in the admin UI) succeeds or the service restarts. The error is recorded in module state, so pages see it in the result envelope. A manual update does not ask `Enabled`; when it succeeds it resets the backoff, and the next scheduled update is an interval (or the delay set with `NextUpdate`) after it.

### Dependencies

Dependencies come through the context: `module.Database(ctx)`, `module.Logger(ctx)`, `module.HTTPClient(ctx)` (30 s timeout; calls are traced as children of the update or query), `module.Clock(ctx)`. The service puts them on the context it passes to `Initialize`, `Update` and query functions. `module.SaveSnapshot(ctx, v)` (from `Update`) and `module.LoadSnapshot(ctx, &v)` store and read the module's snapshot (see [State](#state)). `module.NextUpdate(ctx, d)` (from `Update`) runs the next scheduled update `d` after this one instead of an interval later, for modules that poll adaptively; it applies when `Update` succeeds, scheduled or manual (the most recent success decides), the last call wins, and it has no effect on the backoff after a failure. A delay longer than `stale_after` shows the data as stale while the module waits.

### Shared values

A value several modules use, such as an API client, is defined once with `module.DefineShared` and configured once, under `modules.<name>` like a module. Modules that use it declare it with `module.WithShared`, optionally with declarations of type `D` (e.g. the API scopes they need), and read it from their context with `Get`:

```go
package esi

type Scopes []string

var Shared = module.DefineShared("eveonline_esi", defaultConfig,
	func(ctx context.Context, cfg *config, decls []Scopes) (*Client, error) { ... },
	module.SharedMigrations(migrations))
```

```go
package skills

var Module = module.Define[skills, config]("eveonline_skills", defaultConfig,
	module.WithShared(esi.Shared, esi.Scopes{"esi-skills.read_skills.v1"}))

func (*skills) Update(ctx context.Context) error {
	resp, err := getskills.Request(ctx, esi.Shared.Get(ctx), ...)
	...
}
```

- `Load` builds each shared value once, after migrations and before any `Initialize`, and only if a registered module declares it. The build function gets the decoded config, every declaring module's declarations in module name order, and a context with `Logger`, `HTTPClient` and `Clock`; like `Initialize`, it does no network I/O.
- `SharedMigrations` gives a shared value tables, migrated like a module's with the shared value's name as owner. It reaches them through `module.Database(ctx)` of the context it is called with: the writer in a module's or its own `Update`, the read-only pool in queries and `Version`.
- A shared value that is an `http.Handler` is served on the admin listener under `/<name>/` (the prefix stripped), with `Database` (the writer), `Logger`, `HTTPClient` and `Clock` on the request context, and linked from the admin index under Shared. This is how logins work.
- The config section follows the module rules (defaults, unknown keys stop startup); it has a `schedule` key only when the value has a schedule (below). A section for a shared value no module declares is an unknown module.
- A build error turns off every module that declares the value, as an `Initialize` error does; the error is prefixed with the shared value's name.
- `Get` in a module that did not declare the value panics, which surfaces as an error from that call. A shared value's name may not be a module's name.
- A shared value that implements `module.Versioned` reports the version of the source it serves (`Version(ctx)`, with the read-only pool as `Database`); the admin UI lists every shared value with its version and build error under Shared.
- Shared values have no snapshot or queries.

`module.SharedSchedule(interval, staleAfter)` gives a shared value a schedule; it must implement `module.Updater` (`Update(ctx) error`), checked when it is defined. It runs like a module under the shared value's name: same status in the admin UI, backoff, `ErrAuth`, Update now, `module_state` row, metrics, 2-minute update timeout and `schedule` config key. `module.SharedUpdateTimeout(d)` raises the timeout. It is always enabled. A build error lists it as failed to initialize. The SDE uses this to check for new builds.

### EVE Online

The EVE Online modules share two values, each in a subpackage of `modules/eveonline`:

- `esi.Shared` (`eveonline_esi`): the ESI client and the characters logged in through EVE SSO.
- `sde.Shared` (`eveonline_sde`): the static data, see [Static data](#static-data).

#### ESI

`esi.Shared` (`*esi.Client`, package `modules/eveonline/esi`) sends requests through `lib-esi-go`'s transport on top of the service's HTTP client transport, so ESI calls are traced like other calls; `*Client` is the sender for `lib-esi-go` requests. Its version in the admin UI is the ESI compatibility date requests are sent with (`lib-esi-go`'s `CompatibilityDate`). Config keys under `modules.eveonline_esi`:

| Key | Default | Meaning |
| --- | --- | --- |
| `contact` | none | Contact values in the User-Agent, e.g. `mailto:you@example.com`. Required: without it the client is not built and the EVE modules are off. |
| `client_id`, `client_secret` | empty | The SSO application from developers.eveonline.com. Empty disables character logins. |
| `callback_url` | empty | The application's callback URL: the admin listener's address with path `/eveonline_esi/callback`, as the browser reaches it. Set together with the client keys. |

Characters:

- Modules declare the scopes they need: `module.WithShared(esi.Shared, esi.Scopes{...})`. A login asks for every declared scope.
- `/eveonline_esi/` on the admin listener lists the characters with the declared scopes they have not granted, their token expiry and a rejected refresh. "Log in a character" goes to EVE SSO; the callback stores the character, its granted scopes and tokens in `eveonline_esi_characters`. Logging in a character again replaces its tokens and scopes and clears its error. Pending logins live in memory for 10 minutes.
- `Client.Characters(ctx, scopes)` returns the characters that granted all of `scopes`; a module is typically enabled when there is one. `Client.Token(ctx, id)` returns the character's token for `authentication.WithToken`. It refreshes itself a minute before expiry and stores the result, so it is used from `Update`. Refreshes are serialized per character, as a refresh may rotate the refresh token.
- A rejected refresh (`invalid_grant`, `invalid_client`) is recorded on the character and fails the update with `module.ErrAuth`. After logging in again, Update now on each affected module resumes it.
- The access token's claims (character ID, name, scopes) are read without checking the signature: the token comes straight from the token endpoint over TLS, authenticated with the client secret.

#### Static data

`sde.Shared` (`*sde.Store`, package `modules/eveonline/sde`, no config keys) imports the datasets modules declare from the Static Data Export into typed tables, checking for a new build every hour, and answers lookups such as `Types(ctx, ids)`. Its version in the admin UI is the imported build. Datasets, tables, update rules and the test fake: [sde.md](sde.md).

#### Server status

`eveonline_status` (`modules/eveonline/status`) has no config keys. `Update` runs every 5 minutes (ESI caches `/status` for 30 s; at most once a minute is reasonable); data is stale after 30 minutes. While the cluster is offline ESI does not answer `/status`: a request that hits lib-esi-go's 10 s timeout (ESI's own), or a 503 or 504 response, is stored as offline (`online: false`, other fields zero, `startTime` null) rather than failing the update. Other errors fail the update. Each successful update also stores a sample (time, online, players) in `eveonline_status_samples`, kept indefinitely: about 105,000 rows a year at the default interval.

Queries:

- `eveonline_status.server` takes no params and returns the stored status.
- `eveonline_status.history` returns the player count over time, one point per step, oldest first: `hours` (default 24, at most 2160) and `stepMinutes` (default 5), at most 2000 points. Steps align to multiples of the step, and the last one holds now. A point is the rounded average of the online samples in its step, or null when the server was offline or not polled throughout it, so a chart shows downtime as a gap.

#### Skill queues

`eveonline_skills` (`modules/eveonline/skills`) has no config keys. It declares scopes `esi-skills.read_skillqueue.v1` and `esi-skills.read_skills.v1` and is enabled when a logged-in character granted both. It declares `sde.Types` for skill names. `Update` runs every 15 minutes; data is stale after 2 hours. ESI caches both for 60 s, but they only change in game: queries count down from the stored finish dates, so more frequent updates add nothing.

- `Update` fetches every such character's queue, ordered by queue position, and trained skills with total and unallocated skill points, and stores them as ESI returned them. A character whose requests fail keeps its previous data and fetch time, with the error; the update fails only when every character fails, so one rejected token does not pause the others. It fails with `module.ErrAuth` only when every character's refresh was rejected.
- Skill names come from the SDE when the query runs, so a new SDE build names skills without a skills update. A skill the imported build does not have, or any skill before the first import, has an empty name; the page shows `Skill <id>`.
- ESI keeps finished skills in the queue, and does not add them to `/skills`, until the character logs in. Queries drop entries whose finish date has passed, using the current time rather than the update's, so a queue runs out on screen between updates, and add their points (the entry's end points less the skill's stored points) to the total.
- A paused queue has entries without start and finish dates.

Query `eveonline_skills.queue` takes `skills` (upcoming skills per character, default 5, at most 50) and returns the characters by name: `paused`, `ends` (finish of the last skill; null when empty or paused), `remaining` (skills not finished), `skills` (the first upcoming ones, the one in training first, with level, start and finish), `totalSp`, `unallocatedSp`, `fetchedAt` and `error` per character.

## Storage

One SQLite database, `dashboard.sqlite` in `data_dir`, pure-Go driver (`modernc.org/sqlite`), no cgo.

### SQL files

SQL lives in `.sql` files next to the code that owns it:

```
module/sql/migrations/00001_state.sql
module/sql/queries.sql
device/sql/migrations/00001_keys.sql
device/sql/queries.sql
modules/weather/sql/migrations/00001_locations.sql
modules/weather/sql/queries.sql
modules/eveonline/status/sql/migrations/00001_samples.sql
modules/eveonline/status/sql/queries.sql
modules/eveonline/esi/sql/migrations/00001_characters.sql
modules/eveonline/esi/sql/queries.sql
modules/eveonline/sde/sql/migrations/00001_sde.sql
modules/eveonline/sde/sql/queries.sql
sqlc.yaml                       one entry per owner
```

- **Migrations** are goose files (`-- +goose Up`), embedded with `go:embed`. Each owner has its own sequence and its own version table (`goose_storage`, `goose_device`, `goose_weather`), run through a goose `Provider` per owner, so modules number their migrations independently. `module.Load` runs the module state migrations (owner `storage`), then each shared value's, then each module's, before any `Initialize`. `device.Load` runs the API key migrations (owner `device`). A failing migration stops startup.
- **Queries** are written in `queries.sql` and turned into Go by sqlc. `sqlc.yaml` at the repository root has one entry per owner whose schema is that owner's migrations; adding a module adds an entry. Generated files are committed; CI checks they are up to date (`go tool sqlc diff`); sqlc is tracked with the `tool` directive in `go.mod`.
- **Tables** are prefixed with their owner (`weather_locations`, `device_keys`); module state is `module_state`. An owner never reads or writes another owner's tables. Shared data goes through service APIs: module state through `module.SaveSnapshot` and `module.LoadSnapshot`.
- **Module tables are for low-rate data**: caches, tokens, geocoding, snapshots. They are written directly by `Update`. Module state (`attempted_at`, `fetched_at`) is written directly on each update; at intervals of a minute or more that is accepted.
- **Times** are `TIMESTAMP` columns in UTC, stored as SQLite date text (driver option `_time_format=sqlite`), so SQLite's date functions work on them.

Registration hands the migrations to the service; `WithMigrations` takes the embedded FS and uses its `sql/migrations` directory as the goose root:

```go
//go:embed sql/migrations/*.sql
var migrations embed.FS

var Module = module.Define[weather, config]("weather", defaultConfig,
	module.WithMigrations(migrations),
)
```

### Connections

- **Page size**: on a new file, a bootstrap connection runs `page_size=16384` and then `journal_mode=WAL`; the WAL switch writes the header with that page size, and after that it cannot change. The driver applies DSN pragmas in alphabetical order, so it cannot share a DSN with `journal_mode`. A test checks the page size.
- **Writer**: one connection (`MaxOpenConns(1)`), used by `Initialize`, `Update`, module state, device API keys and migrations. Code holding a writer transaction must not ask for the writer again: with one connection that deadlocks.
- **Reader**: a pool of 4 opened with `query_only(1)`, used by query functions and status reads. `module.Database(ctx)` returns the writer in `Initialize` and `Update` and the reader in queries, so a query cannot write.
- DSN pragmas apply to every new pooled connection: `busy_timeout=5000`, `foreign_keys=ON`, `cache_size=-32768` (32 MB per connection, so up to 160 MB), `mmap_size=268435456` (256 MB, shared); the writer also sets `journal_mode=WAL` and `synchronous=NORMAL`. Driver options on both: `_time_format=sqlite` and `_timezone=UTC`, so every time is converted to UTC before it is written.

### Write load

The concern is many tiny writes wearing the SSD. SQLite runs in WAL mode with `synchronous=NORMAL`, a large page cache and `mmap_size` (reads come from memory), `busy_timeout`, and one writer connection. WAL checkpoints at the default 1000 pages, so they are infrequent. Rendered PNGs, device wake data and renderer state stay in memory.

ZFS: put `/data` on its own dataset with `recordsize=16K`, matching SQLite `page_size=16384`. ZFS' default 128K records would rewrite 128K for every changed 4K page.

Backups: ZFS snapshots of the `/data` dataset. WAL mode keeps the file crash-consistent, so a snapshot restores like a power loss.

### State

The service keeps, per module, the latest status in `module_state`: `fetched_at`, `attempted_at`, `error`, plus a JSON document (`data`) for modules whose data is one snapshot (weather). A snapshot saved during `Update` is written together with `fetched_at` when `Update` returns nil, and dropped when it fails. Queries return data with its freshness (age above `stale_after` marks it stale).

Modules that need more (caches, tokens, history) define their own tables in their [SQL files](#sql-files).

## Scheduler

Module updates: per-module interval, jitter on start, timeout and backoff as in [Configuration and lifecycle](#configuration-and-lifecycle). Scheduled and manual updates of one module never run at the same time.

## Devices

### Configuration

```yaml
devices:
  hallway:
    mac: "AA:BB:CC:DD:EE:FF"
    rotation: 0          # 0, 90, 180, 270
    layout: e1001        # folder under pages/layouts/
    pages: [weather, hello]
    params:              # optional query param overrides, <page>.<data key>
      weather.weather: {days: 5}
```

A layout is a folder of pages, `pages/layouts/<layout>/<page>.tsx`, written for one panel size; a page is named `<layout>/<page>`. Each device uses one layout and lists pages from it; the first page is the main page. A page missing from the layout stops startup. Components and helpers shared by layouts live in `pages/components/` and `pages/lib/`.

Device names and layouts may contain letters, digits, `-` and `_`; `page` is reserved as a device name (it is taken by the admin route `/preview/page/...`). MACs must be unique.

The model comes from the device's `Model` header (`reterminal_e1001`, `reterminal_e1003`); the panel size follows from the model. An unknown model is ignored, and a device that has not reported one yet is treated as `reterminal_e1001`. Both models get the same 4-gray treatment: the E1003 is rendered at its own resolution but with the E1001's four gray levels, so one set of styles works on both.

### Screen schedule

Global, under `schedule`: a wake every `interval` from `first_wake` up to and including `last_wake`, in local time; the last wake sleeps until the first. The default is hourly from 07:00 to 00:00: 18 timer wakes a day plus button presses. A `last_wake` at or before `first_wake` is on the next day.

| Key | Default | Meaning |
| --- | --- | --- |
| `first_wake` | `"07:00"` | First timer wake, `HH:MM` |
| `last_wake` | `"00:00"` | Last timer wake, `HH:MM` |
| `interval` | `1h` | Whole minutes, from 1m to 24h |
| `detail_timeout` | `5m` | How long a page shown after a button press stays up |

`refresh_rate` is computed from the server clock on every request:

- **Main page**: seconds until the next wake time. If that is 60 s or less (a device that woke just before a wake time), use the wake after it: it already shows the current data. This absorbs the firmware starting its sleep timer only after drawing, and clock drift.
- **Detail page** (after a button press): `detail_timeout`, or until the next wake time if that is sooner. The following timer wake shows the main page.

DST is handled by computing wake times in local time; the night sleep is 6 or 8 hours on transition nights. A wake in a skipped hour moves an hour later (merging with a wake already there); a wake in a repeated hour happens once, on the second pass.

### Buttons

Single click only. A button wake (`Update-Source` is `button` or `EXT0`) refreshes and shows the next page, wrapping around; any other wake (`timer`, `powercycle`) shows the main page. The firmware does not report which button was pressed, and on the ESP32-S3 only one button can wake the unit.

The current page index per device is kept in memory; after a service restart a device starts at the main page.

Firmware hold behavior, outside the service's control: 1-5 s hold is a special function, more than 5 s resets Wi-Fi, 15 s or more wipes the device's API key and runs setup again.

### Orientation

The firmware does not report orientation. It does rotate automatically: a PNG with width and height swapped relative to the panel is drawn in portrait. Orientation is therefore a device setting:

| `rotation` | Server renders | Notes |
| --- | --- | --- |
| 0 | native size (E1003: 1872x1404) | landscape |
| 90 | swapped size (1404x1872) | firmware rotates to portrait |
| 180 | native size, image turned 180° | landscape, upside down mount |
| 270 | swapped size, image turned 180° | portrait, other direction |

Pages get the resulting viewport as a prop (`viewport.width`, `viewport.height`, `viewport.portrait`). The firmware's rotation direction is unverified on hardware.

### Protocol

TRMNL BYOS, on the device listener. Devices are identified by the `ID` header (their MAC).

| Route | Behavior |
| --- | --- |
| `GET /api/setup` | Known MAC: HTTP 200 with `status: 200`, `api_key`, `friendly_id` (last three MAC bytes), `message`. The key is created on the MAC's first setup and stored in the database (`device_keys`); later setups return the same key. Unknown MAC, or a missing or invalid `ID`: HTTP 200 with JSON `status: 404`; the device shows "MAC not registered" and retries every 5 minutes. Unknown MACs are listed as pending in the admin UI (up to 32). A database error is HTTP 503. |
| `GET /api/display` | Authenticate by `Access-Token` against the stored key (constant-time compare), record the wake headers, pick the page, render, return `image_url`, `filename`, `refresh_rate` (plus `status: 0`, `reset_firmware: false`, `update_firmware: false`, `special_function: "sleep"`). |
| `POST /api/log` | Authenticated like `/api/display`. The body (up to 64 KB) is written to the service log; HTTP 204. Nothing is stored. |
| `GET /images/{filename}.png` | Rendered image, from memory. The device listener keeps the 16 most recent images. |

- `image_url` is absolute: `public_url` in the config is the base the devices can reach.
- `filename` is 23 characters: a 12-hex hash of the rendered PNG plus a Unix timestamp (`3fa9c2e81b07-1792087200`). The firmware truncates names over 31 characters and purges cached names without a 10-digit timestamp suffix.
- Never return JSON `status: 500`: firmware treats it as a reset and wipes its credentials. A failed render returns the device's previous filename (and image) if there is one, and HTTP 503 otherwise; database errors are HTTP 503.
- When the service is down, devices retry every 5 minutes until it is back. Accepted.
- Authentication failures on `/api/display` and `/api/log`: an unknown MAC or a missing `Access-Token` is HTTP 404 with JSON `status: 404`; a known MAC whose token does not match the stored key, or that has no stored key, is HTTP 401 with JSON `status: 401`. The firmware keeps its credentials on either and retries on its next wake; it only clears them (and Wi-Fi) on JSON `status: 500`. A device whose key the service does not have (a device set up against a database without its key, or a lost database) runs setup again after a 15 s button hold.
- The setup response hands the key to whoever presents a configured MAC: the TRMNL protocol has no other identity. Keep the device listener on a trusted network.

### Wake data

Each `/api/display` records, in memory, the wake source (`Update-Source`), battery voltage (`Battery-Voltage`), RSSI (`RSSI`), firmware version (`FW-Version`) and the time. Pages get it as `device.wake`; the admin UI shows the last wake per device. It is lost on restart.

## Rendering

### Page files

Pages are React components in TSX, styled with CSS modules, rendered by Takumi through `takumi-js` (no browser). A page exports the data it needs and a default component that receives the results as props:

```tsx
// pages/layouts/e1001/weather.tsx (abridged)
import { LineChart } from "../../components/LineChart";
import { query, type PageProps } from "../../lib/page";
import styles from "./weather.module.css";

export const data = {
  weather: query("weather.forecast", { hours: 24, days: 3 }),
};

type Data = typeof data;

export default function Weather({ data, viewport, timezone }: PageProps<Data>) {
  const { value: f, fetchedAt, stale, error } = data.weather;
  if (!f) return <div className={styles.screen}>Geen weerbericht{error ? `: ${error}` : ""}</div>;
  return (
    <div className={styles.screen}>
      <LineChart values={f.hourly.map((h) => h.temperature)} width={viewport.width - 64} height={120} />
    </div>
  );
}
```

- `data` is static: `query()` only builds a descriptor (`{module, query, params}`). Components never fetch; everything arrives as props.
- Each `data.<key>` is a result envelope: `{value, fetchedAt, stale, error}`. Pages decide how to show stale or failed data.
- Props are `data`, `device` (`name`, `model`, `rotation`, `wake`), `viewport`, `system` (`staleModules`: modules whose data is stale), `now` and `timezone`.
- `PageProps<Data>` types each key from the generated query types.
- CSS modules: `import styles from "./x.module.css"`; Bun's bundler scopes class names and emits one CSS file per page. Takumi supports a CSS subset: class selectors, flexbox and grid, borders, fonts. Complex selectors and some properties may not render.
- Inline `<svg>` works: charts are components that emit SVG.

### Queries

Modules expose named, typed queries; this is how stored data reaches pages.

```go
type forecastParams struct {
	Hours int `json:"hours"`
	Days  int `json:"days"`
}

var Module = module.Define[weather, config](
	"weather",
	defaultConfig,
	module.WithQuery("forecast", (*weather).forecast),
)

func (m *weather) forecast(ctx context.Context, p forecastParams) (*Forecast, error) {
	var f Forecast
	ok, err := module.LoadSnapshot(ctx, &f)
	// trim to p.Hours and p.Days from now
}
```

- Params are the page's declared params, with the device's overrides merged over them, decoded as JSON into the query's own struct. Unknown keys and bad values fail at startup (for every page and every device override), not at render.
- Results are plain structs that serialize to JSON; TypeScript types for them are generated (see [Types](#types)).
- Queries read storage only, never the source: their own tables through sqlc and module state, on the read-only connection.

### Renderer process

A long-running Bun process (`bun renderer/main.ts --pages pages [--watch]`, run in `renderer.dir`), started on the first request and supervised by the Go service. Per-render process start would cost 50-100 ms; a resident process renders in ~20 ms (measured, 800x480).

- **Bundling**: at start the renderer bundles every `pages/layouts/<layout>/<page>.tsx` with `Bun.build` into JS plus CSS. With `renderer.watch` on, for development, it rebuilds on file changes; a failed rebuild keeps the previous bundle.
- **Protocol**: length-prefixed frames over stdin/stdout, one request at a time. Each frame is a 4-byte big-endian length, then a JSON header, then for render responses the raw PNG bytes (header carries the PNG length). No base64, no line-length limits. Covered by tests on both sides.

| Request | Response |
| --- | --- |
| `{"id":1,"op":"describe"}` | `{"id":1,"pages":[{"name":"e1001/weather","data":{...}}]}` |
| `{"id":2,"op":"render","page":"e1001/weather","props":{...}}` | header `{"id":2,"png":<bytes>}` + PNG, or `{"id":2,"error":"..."}` |

- **Logging**: stdout is reserved for frames; the renderer redirects `console.log` to stderr at start, and Go logs stderr lines.
- **Supervision**: a process that exits is replaced on the next request, with backoff from 500 ms to 30 s between starts. Each request has its own timeout, 30 s for a render and 60 s for describe (it may bundle); a process that does not answer in time is killed and restarted, so requests queued behind it are not stuck. A request in flight when the process dies fails; the device keeps its last image and gets it again on the next wake.
- **Callers leaving**: when a caller's context ends (a device disconnects), the caller gets its error at once, while the response is still read in the background, within the same timeout, before the next request uses the process. Only a response that does not arrive in time restarts the process.

### Pipeline

1. **Describe**: the renderer reports pages and their `data`. Go asks on every use, so a watch rebuild is seen by the next request. At startup Go checks that every page's `module.query` exists and its params decode into the query's params type, with each device's overrides too; errors stop startup. Pages from a later rebuild are not checked again: a bad query shows as an error envelope.
2. **Resolve**: Go runs the page's queries concurrently with a 10 s timeout. One failing query yields an error envelope, not a failed page.
3. **Render**: Go sends props to the renderer, gets an RGBA PNG at `props.viewport`.
4. **Reduce** (Go): rotate if needed, map each pixel to the nearest of 4 gray levels (0, 85, 170, 255) without dithering, which keeps text sharp, and encode a 2-bit PNG. Both models use 4 levels.
5. **Check**: the PNG must be under 750 KB (both models; firmware limit with PSRAM). A 2-bit image without dithering is far below that (spike: 34 KB at 1872x1404); exceeding it fails the render, which is logged, and the device gets its previous image.
6. **Name**: `filename` is the PNG's hash plus timestamp (see [Protocol](#protocol)). An image identical to the device's previous one keeps its filename and the device skips the download. Props include `now` and wake values, so most wakes produce a new image; that is accepted.

### Types

Go query types are the source of truth. `cmd/gentypes` walks every registered query's params and result types by reflection and writes `pages/generated/queries.ts`: one interface per struct (named module + Go type name, e.g. `WeatherForecast`), and a `Queries` map from `"module.query"` to `{params, result}`. `query()` and `PageProps` use it, so a typo in a query name, a wrong param or a wrong field is a type error in the editor and in CI (`tsc --noEmit`). The generated file is committed; CI checks it is up to date.

Mapping rules: `time.Time` is a string (RFC 3339), pointers are `T | null`, and exported fields are named by their `json` tag (`omitempty` and `omitzero` make them optional). Slices are typed without `null`, so queries return non-nil ones. Params fields are all optional: a missing param is the Go zero value, which a query treats as its default. Embedded fields, maps, `[]byte`, interfaces, the `,string` option and types with their own JSON encoding are errors; gentypes supports a shape once a query needs it.

Pages declare `type Data = typeof data` and use `PageProps<Data>`: destructuring `data` from props would otherwise shadow the module's `data` in its own type.

### Shared code

- `pages/lib/`: `page.ts` (`query`, `PageProps`, prop types), `format.ts` (Dutch dates, times, short day names, temperatures and millimeters with a decimal comma), `weather.ts` (weather code labels, wind direction), `eveonline.ts` (skill levels, skill queues by urgency). Pure functions.
- `pages/components/`: `LineChart` (a line over optional bars, as inline SVG), `SkillQueues` (each character's skill queue, most urgent first, in the width and height it is given: skill points and as many upcoming skills per character as fit, none when only the queue ends fit, more columns when the rows do not; narrow columns show how long a queue lasts without its end time).

Time: the config's `timezone` is the single source. Go loads it with `time/tzdata` embedded, computes everything in it, and passes `now` and all timestamps to pages as ISO 8601 strings with offset, plus `timezone` in props. Formatting uses `Intl` with `nl-NL`; a `bun test` asserts the runtime's ICU data formats Dutch dates as expected.

### Testing

Each step has its own test with no network:

| What | Tool | How |
| --- | --- | --- |
| Module `Update` and queries | `go test` | `httptest` fake of Open-Meteo with fixtures in `modules/weather/testdata`, storage in a temp dir, assertions on the query results |
| EVE Online shared values and modules | `go test` | `moduletest.HTTPClient(handler)` serves every request in-process (works inside a synctest bubble); `sde.NewFake` serves SDE builds |
| Module loading, scheduler | `go test` | fake modules: config errors, panics, backoff, auth pause, manual updates, snapshots |
| Storage | `go test` | pragmas and page size, reader cannot write, per-owner migrations |
| Device protocol | `go test` | setup, keys surviving a restart, auth, page selection, refresh rate (including DST), render failure, image store |
| Pipeline, renderer process | `go test` | fake renderer and queries, including failing and stale ones; frame encoding; timeouts, restarts, backoff; one integration test against the real Bun renderer |
| Service and admin UI | `go test` | config parsing, startup checks, previews, simulator, `/healthz`, `/metrics`, request spans, shutdown drain |
| Types | `go test`, `tsc --noEmit` | gentypes against a golden file; pages against generated types |
| Formatting, renderer frames | `bun test` | table tests on pure functions; frame encoding and rendering in `renderer/` |

### Fonts

Takumi's built-in sans-serif, a variable font covering all weights. No font files are loaded.

## Admin UI

On a separate admin listener. No authentication; access is restricted at the network level (bind addresses in config, TrueNAS port mapping, firewall rules).

| Route | Purpose |
| --- | --- |
| `GET /` | Module status (phase, interval, last fetch and attempt, next run, error, Update now), shared values with their version, devices with their last wake and page previews, pending devices, every page on every panel at rotations 0 and 90 |
| `POST /modules/{module}/update` | Run one update of the module now, then redirect to `/` |
| `GET /preview/{device}/{layout}/{page}.png` | Exactly what the device gets; `?model=` overrides the device's model |
| `GET /preview/{device}/{layout}/{page}/props.json` | The props the page receives; `?model=` as above |
| `GET /preview/page/{layout}/{page}.png?model=...&rotation=...` | A page on a panel without a device; model defaults to `reterminal_e1001`, rotation to 0 |
| `GET /simulate/{device}` | Simulator page for a device |
| `POST /simulate/{device}/wake?source=timer\|button&model=...` | Plays a device wake (setup if no key yet, then display) through the device protocol in-process; returns the requests and responses as JSON |
| `GET /images/{filename}.png` | Images rendered for simulated wakes |
| `GET /{shared}/...` | Pages of shared values that are `http.Handler`s, e.g. `/eveonline_esi/` (characters, SSO login and callback); linked from `/` under Shared |
| `GET /healthz` | Liveness, `ok` |
| `GET /metrics` | Prometheus metrics, see [Observability](#observability) |

The simulator runs on its own `device.Devices`, mounted only on the admin listener: its page, wake and image store (4 images) are separate from the real devices', so it never evicts their images. It shares the stored API keys, which belong to a MAC rather than to a device's state. The device listener has no simulator mode.

## Observability

### Metrics

Prometheus metrics on the admin listener at `/metrics`, registered with `promauto` in the package that records them, next to the Go runtime and process metrics.

| Metric | Labels | Meaning |
| --- | --- | --- |
| `dashboard_http_request_duration_seconds` | `listener`, `route`, `code` | Request duration; `route` is the matched `ServeMux` pattern, or `unmatched` |
| `dashboard_module_updates_total` | `module`, `result` | Updates by result: `success`, `failure`, `auth` |
| `dashboard_module_update_duration_seconds` | `module` | Update duration |
| `dashboard_module_last_success_timestamp_seconds` | `module` | Last successful update |
| `dashboard_render_duration_seconds` | `page`, `result` | Page render from resolving data to the finished image; an unknown page is `unknown` |
| `dashboard_device_wakes_total` | `device`, `source` | Display requests by `Update-Source`: `timer`, `button`, `EXT0`, or `other` |
| `dashboard_device_battery_volts` | `device` | `Battery-Voltage` of the latest wake |
| `dashboard_device_rssi_dbm` | `device` | `RSSI` of the latest wake |
| `dashboard_device_last_wake_timestamp_seconds` | `device` | Latest wake |

Simulated wakes are not counted in the device metrics.

### Traces

OpenTelemetry traces, exported over OTLP/HTTP when `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` is set; without either, spans are dropped. The other standard `OTEL_*` variables apply, e.g. `OTEL_SERVICE_NAME` (default `dashboard`). Incoming `traceparent` headers are honored.

| Span | Starts a trace | Children |
| --- | --- | --- |
| `<METHOD> <pattern>`, per request on both listeners, except `/healthz` and `/metrics` | yes | `render <page>` with a `<module>.<query>` span per page query and the module's outgoing HTTP calls |
| `<module>.update`, per scheduled update | yes | the module's outgoing HTTP calls |

A manual update (`POST /modules/{module}/update`) is a child of its request. Spans of failed updates, renders and queries have error status. A failing `Enabled` is logged and recorded in module state, but has no span or update metric.

## Configuration

One YAML file, `-config` flag (default `/config/dashboard.yaml`). `${VAR}` references are substituted from the environment, so secrets can be env vars or inline values; a referenced variable that is not set stops startup. A substituted value takes its YAML type (`${PORT}` can be a number), except null: an empty, `null` or `~` value is that string, not a missing key that falls back to the default. Unknown keys stop startup.

```yaml
listen:
  device: "0.0.0.0:8080"       # default ":8080"
  admin: "192.168.1.10:8081"   # default "127.0.0.1:8081"; bind to a trusted interface only
public_url: http://192.168.1.10:8080   # required; base for image_url, reachable from the devices
timezone: Europe/Amsterdam     # default
data_dir: /data                # default

schedule:                      # defaults shown
  first_wake: "07:00"
  last_wake: "00:00"
  interval: 1h
  detail_timeout: 5m

renderer:                      # defaults shown
  bun: bun                     # Bun executable
  dir: .                       # holds renderer/, pages/ and node_modules
  watch: false                 # rebuild pages on change, for development

modules:
  weather:
    location: "Uithoorn, The Netherlands"
    model: knmi_seamless

devices:
  hallway:
    mac: "AA:BB:CC:DD:EE:FF"
    rotation: 0
    layout: e1001
    pages: [weather, hello]
    params:
      weather.weather: {days: 5}
```

Runtime state (device API keys, module state and caches) lives in the database, not the config file.

## Shutdown

On SIGTERM or interrupt: module schedules stop, both listeners stop accepting, and requests in flight get 5 s to finish (within Docker's 10 s stop timeout). Requests still running then have their context canceled, which ends renders and manual module updates; the renderer and storage close only after every handler has returned. A drain that runs out is logged as a warning, not a failed exit. Pending spans get another 5 s to export.

## CI

GitHub Actions (`.github/workflows/ci.yml`) on pushes to `main` and on pull requests: `go vet ./...`, `go run ./cmd/gentypes` with no diff in `pages/generated`, `go test -race ./...` (with Bun installed for the renderer integration test), `bun test`, `bunx tsc --noEmit` and `go tool sqlc diff`.

## Unverified on hardware

`Update-Source` values for a button wake, which button wakes each model, and the firmware rotation direction.
