# Design: dashboard service

Status: ready, 2026-10-16. Background and data source research: [research.md](research.md).

## Overview

One Go service, `dashboard`, serves two Seeed reTerminal e-ink units (E1001, E1003) running TRMNL firmware in BYOS mode. The service owns I/O, storage, scheduling, rendering and the device protocol. Data sources are modules: packages under `modules/` that register themselves, are configured in YAML, and are global to the system (not tied to a device).

```
             +---------------------------- dashboard -----------------------------+
             |                                                                    |
 P1 dongle --+--> modules/p1 -------+                                             |
 S-Miles   --+--> modules/solar ----+                                             |
 Google    --+--> modules/calendar -+--> storage <-- queries --> render ------------+--> device API <-- E1001
 Pleos     --+--> modules/car ------+   (state +        |         (Bun: TSX+Takumi)|               <-- E1003
 Open-Meteo -+--> modules/weather --+    series)        |                           |
             |          ^                                +--> web UI: status, preview
             |      scheduler                                                       |
             +--------------------------------------------------------------------+
```

## Code layout

```
cmd/dashboard/main.go      entrypoint: load config, build service, run
cmd/dashboard/config.go    config loading: YAML file, ${ENV} substitution, validation
service/service.go         the service: wires storage, scheduler, modules, listeners
service/modules/           registration API and the module contract
service/storage/           SQLite: connections, migrations, state, series, rollups, devices
service/storage/sql/       the service's own migrations and queries
service/scheduler/         module updates, rollups, retention
service/device/            TRMNL protocol, health, page selection, screen schedule
service/render/            page pipeline, renderer process, gray reduction, PNG
service/web/               admin UI, preview, module route mounting
modules/modules.go         blank-imports every module so it registers
modules/<name>/            one module per data source
modules/<name>/sql/        the module's migrations and queries
renderer/                  Bun process: bundles pages, renders with takumi-js
pages/                     TSX pages, components, CSS modules
pages/generated/           TypeScript types generated from Go query results
```

Everything outside `modules/` is the service. `service/service.go` imports `modules` so all modules are linked in; it only knows them through the registry.

## Modules

| Module | Source | Data |
| --- | --- | --- |
| `p1` | Smartstuff P1 dongle, local HTTP | state + series (import/export power, energy, gas) |
| `solar` | Hoymiles: S-Miles Cloud now, DTU Modbus later | state + series (production per inverter, alarms) |
| `calendar` | Google Calendar secret iCal URL | state (upcoming events) |
| `car` | Pleos Vehicle Data API, fallback Bluelink | state (SoC, range, charging, locks) |
| `weather` | Open-Meteo | state (now, hourly, daily) |

Later candidates: Plex recently added, waste collection, public transport.

Device health is a service concern, not a module (see [Devices](#devices)).

### Registration

Modules register themselves at init, the same pattern as `database/sql` drivers:

```go
package weather

import "github.com/xaroth/dashboard/service/modules"

type config struct {
	Location string `yaml:"location"`
}

func defaultConfig() *config {
	return &config{Location: "Amsterdam, The Netherlands"}
}

type module struct {
	cfg *config
}

var _ = modules.Register[module, config](
	"weather",
	defaultConfig,
	modules.WithUpdateInterval(15*time.Minute),
	modules.WithStaleAfter(2*time.Hour),
)

func (m *module) Initialize(ctx context.Context, cfg *config) error {
	m.cfg = cfg
	return nil
}

func (m *module) Enabled(ctx context.Context) bool {
	return m.cfg.Location != ""
}

func (m *module) Update(ctx context.Context) error {
	db := modules.Database(ctx)
	// on first run, geocode Location through Open-Meteo and cache the coordinates
	// in the module's table; a changed Location triggers a new lookup.
	// then fetch the forecast and write state.
}
```

`Register` is generic over the module and config types. A third, inferred type parameter constrains `*M` to the module interface, so a missing method is a compile error:

```go
func Register[M any, C any, PM interface {
	*M
	Module[C]
}](name string, defaults func() *C, opts ...Option) Registration
```

The registry is package-level state in `service/modules`, filled before `main` runs; the service reads it once at start.

### Contract

Deliberately small; capabilities are added when a module needs them.

| Method | Purpose |
| --- | --- |
| `Initialize(ctx, *C) error` | Receive config, set up clients. The module's migrations have run by then. No network I/O: it runs for every registered module, configured or not. |
| `Enabled(ctx) bool` | Whether the module's schedule runs. Decided by the module, typically from its config (e.g. `car` is enabled when credentials are set). |
| `Update(ctx) error` | Called by the scheduler: fetch from the source, write to storage. |

Options: `WithUpdateInterval`, `WithStaleAfter`, `WithQuery` (see [Queries](#queries)), `WithMigrations` (see [SQL files](#sql-files)).

Not in v1, added when a module needs it: HTTP routes (OAuth, login forms), active hours, backfill hooks, a shared key-value store.

Update and queries are split so a slow or failing source never blocks a screen: pages render from the last stored data.

### Configuration and lifecycle

- Every registered module is initialized with its defaults, overlaid with its `modules.<name>` section if present. Unknown keys and decode errors stop startup: they are operator errors.
- The scheduler asks `Enabled` before each update. A disabled module skips the update and shows as disabled (not failed) in the web UI; its queries still serve stored data, marked stale once older than `stale_after`. Because it is asked every time, a module can enable itself later, e.g. once a token appears in its table.
- Timing overrides live under a reserved `schedule` key that the service strips before decoding:

```yaml
modules:
  p1:
    url: http://192.168.1.20
    schedule:
      update_interval: 1m
```

- A source being offline never stops the service: network work happens in `Update`, whose errors mark the module failed and are retried with backoff. If `Initialize` returns an error, the module is marked failed and stays off until restart.
- Retries: exponential backoff on errors, not capped at the interval. Authentication errors (401/403) stop retries until the config changes or the service restarts, and show on the screens. Modules return a typed error for this (`modules.ErrAuth`).

### Dependencies

Dependencies come through the context: `modules.Database(ctx)`, `modules.Logger(ctx)`, `modules.HTTPClient(ctx)`, `modules.Clock(ctx)`. The service puts them on the context it passes to `Initialize`, `Update` and query functions. Tests build a context with fakes through the same setters.

## Storage

One SQLite database on the data volume, pure-Go driver (`modernc.org/sqlite`), no cgo, static binary.

### SQL files

SQL lives in `.sql` files next to the code that owns it:

```
service/storage/sql/migrations/00001_state.sql
service/storage/sql/queries.sql
modules/weather/sql/migrations/00001_locations.sql
modules/weather/sql/queries.sql
sqlc.yaml                       one entry per owner
```

- **Migrations** are goose files (`-- +goose Up`), embedded with `go:embed`. Each owner has its own sequence and its own version table (`goose_storage`, `goose_weather`), run through a goose `Provider` per owner, so modules number their migrations independently. The service runs the storage migrations, then each registered module's, before any `Initialize`. A failing migration stops startup.
- **Queries** are written in `queries.sql` and turned into Go by sqlc. `sqlc.yaml` at the repository root has one entry per owner whose schema is that owner's migrations; adding a module adds an entry. Generated files are committed; CI checks they are up to date (`sqlc diff`).
- **Tables** are prefixed with their owner (`weather_locations`). An owner never reads or writes another owner's tables. Shared data goes through service APIs: module state, and series through `modules.Series(ctx)` for writes (buffered) and reads (bucketed, picking the rollup).
- **Module tables are for low-rate data**: caches, tokens, geocoding, snapshots. They are written directly by `Update`. Anything sampled every minute goes through the series buffer, so it lands in the minute flush. Module state (`attempted_at`, `fetched_at`) is written directly on each update; at intervals of a minute or more that is accepted.
- **Times** are `TIMESTAMP` columns in UTC, stored as SQLite date text (driver option `_time_format=sqlite`), so SQLite's date functions work on them. Series use integer Unix seconds to keep rows small.

Registration hands the migrations to the service; `WithMigrations` takes the embedded FS and uses its `sql/migrations` directory as the goose root:

```go
//go:embed sql/migrations/*.sql
var migrations embed.FS

var _ = modules.Register[module, config]("weather", defaultConfig,
	modules.WithMigrations(migrations),
)
```

### Connections

- **Page size**: on a new file, a bootstrap connection runs `page_size=16384` and then `journal_mode=WAL`; the WAL switch writes the header with that page size, and after that it cannot change. The driver applies DSN pragmas in alphabetical order, so it cannot share a DSN with `journal_mode`. A test checks the page size.
- **Writer**: one connection (`MaxOpenConns(1)`), used by `Update`, the minute flush, rollups and migrations. Code holding a writer transaction must not ask for the writer again: with one connection that deadlocks.
- **Reader**: a pool of 4 opened with `query_only(1)` after migrations, used by query functions. `modules.Database(ctx)` returns the writer in `Initialize` and `Update` and the reader in queries, so a query cannot write.
- DSN pragmas apply to every new pooled connection: `journal_mode=WAL` (writer), `synchronous=NORMAL`, `busy_timeout=5000`, `foreign_keys=ON`, `cache_size=-32768` (32 MB per connection, so up to 160 MB), `mmap_size=268435456` (256 MB, shared). Driver options on both: `_time_format=sqlite` and `_timezone=UTC`, so every time is converted to UTC before it is written.

### Write load

The concern is many tiny writes wearing the SSD, not losing a minute of data. So:

- Modules write samples into an in-memory buffer; the service flushes it in one transaction per minute. Losing up to a minute on a crash is accepted. Flush on shutdown (SIGTERM).
- SQLite: WAL mode, `synchronous=NORMAL`, large page cache and `mmap_size` (reads come from memory), `busy_timeout`, one writer connection. WAL checkpoints at the default 1000 pages, so they are infrequent.
- Rendered PNGs, device health of the current wake and renderer state stay in memory. Health series are written with the minute flush.
- ZFS: put `/data` on its own dataset with `recordsize=16K` and SQLite `page_size=16384`. ZFS' default 128K records would rewrite 128K for every changed 4K page.

Estimate: one flush per minute touching a handful of pages is roughly 100-200 KB per minute including WAL and checkpoint, about 50-100 GB per year. Consumer SSDs are rated for hundreds of TB written. To confirm by measuring once running.

Queries read SQLite only, so buffered samples show up within a minute.

Backups: ZFS snapshots of the `/data` dataset. WAL mode keeps the file crash-consistent, so a snapshot restores like a power loss.

### State

The service keeps, per module, the latest status: `fetched_at`, `attempted_at`, `error`, plus a JSON document for modules whose data is one snapshot (weather, calendar, car). Queries return data with its freshness (age above `stale_after` marks it stale).

Modules that need more (caches, tokens, history) define their own tables in their [SQL files](#sql-files).

### Time series

For P1 and solar history graphs and device battery. Schema `series(name, ts, value)` with `ts` in Unix seconds, with rollup tables at 5 minutes, 1 hour and 1 day. Retention: raw 7 days, 5-minute 90 days, hourly and daily forever. Queries pick the coarsest resolution that still fills the graph.

Each series declares a kind, which decides rollups and usage:

| Kind | Example | Rollup | Usage over a period |
| --- | --- | --- | --- |
| `rate` | P1 power (W), solar power (W) | time-weighted average; gaps are not filled | average x covered time |
| `counter` | P1 meter readings (kWh, m³), inverter total Wh | last value | difference; a drop is a reset (meter or inverter replaced): count from the new value |
| `daily_counter` | S-Miles and DTU "today Wh" | last value | value at end of day; resets at local midnight |

- Gaps (P1 reboots, Wi-Fi drops) stay gaps: a bucket without samples has no value and charts show a break. Counters bridge gaps naturally.
- Buckets of an hour or longer align to local time (Europe/Amsterdam); a day has 23 or 25 hours on DST dates.
- Backfill: the P1 dongle's own hour/day/month history can seed the counters on first start. Later, not v1.

## Scheduler

- Module updates: per-module interval, jitter on start, backoff as in [Configuration and lifecycle](#configuration-and-lifecycle).
- Storage jobs: flush buffered writes (every minute), rollups, retention.

## Devices

### Configuration

```yaml
devices:
  hallway:
    mac: "AA:BB:CC:DD:EE:FF"
    rotation: 0          # 0, 90, 180, 270
    pages: [overview, energy, car]
```

The model and native panel size come from the device's own headers (`Model: reterminal_e1001`, `Width`, `Height`), not from config. Both models get the same 4-gray treatment: the E1003 is rendered at its own resolution but with the E1001's four gray levels, so one set of styles works on both.

### Screen schedule

Global: a wake every `interval` from `first_wake` up to and including `last_wake`, in local time; the last wake sleeps until the first. The default is hourly from 07:00 to 00:00: 18 timer wakes a day plus button presses. A `last_wake` at or before `first_wake` is on the next day.

`refresh_rate` is computed from the server clock on every request:

- **Main page**: seconds until the next wake time. If that is 60 s or less (a device that woke just before a wake time), use the wake after it: it already shows the current data. This absorbs the firmware starting its sleep timer only after drawing, and clock drift.
- **Detail page** (after a button press): `detail_timeout` (5 minutes), or until the next wake time if that is sooner. The following timer wake shows the main page.

DST is handled by computing wake times in local time; the night sleep is 6 or 8 hours on transition nights. Wakes in a skipped hour are dropped; in a repeated hour they happen once, on the second pass.

`interval` is whole minutes, from 1m to 24h.

### Buttons

Single click only. A button wake (`Update-Source` is `button` or `EXT0`) refreshes and shows the next page, wrapping around; any other wake (`timer`, `powercycle`) shows the main page. The firmware does not report which button was pressed, and on the ESP32-S3 only one button can wake the unit.

The current page index per device is kept in memory; after a service restart a device starts at the main page.

Firmware hold behavior, outside our control: 1-5 s hold is a special function, more than 5 s resets Wi-Fi, 15 s or more wipes the device's API key and runs setup again. Worth a note near the devices.

### Orientation

The firmware does not report orientation. It does rotate automatically: a PNG with width and height swapped relative to the panel is drawn in portrait. Orientation is therefore a device setting:

| `rotation` | Server renders | Notes |
| --- | --- | --- |
| 0 | native size (E1003: 1872x1404) | landscape |
| 90 | swapped size (1404x1872) | firmware rotates to portrait |
| 180 | native size, image turned 180° | landscape, upside down mount |
| 270 | swapped size, image turned 180° | portrait, other direction |

Pages get the resulting viewport as a prop (`viewport.width`, `viewport.height`, `viewport.portrait`). The firmware's rotation direction is to confirm on hardware.

### Protocol

TRMNL BYOS, on the device listener:

| Route | Behavior |
| --- | --- |
| `GET /api/setup` | Known MAC: return HTTP 200 with `status: 200`, `api_key`, `friendly_id`. Firmware reruns setup unless it has both. Unknown MAC: HTTP 200 with JSON `status: 404`; the device shows "MAC not registered" and retries every 5 minutes. The MAC is listed as pending in the web UI. |
| `GET /api/display` | Authenticate by `Access-Token`, record health headers, pick the page, render if needed, return `image_url`, `filename`, `refresh_rate`. |
| `GET /images/{filename}.png` | Rendered image, from memory. |
| `POST /api/log` | Store device log entries; kept 30 days. |

- `image_url` is absolute: `public_url` in the config is the base the devices can reach.
- `filename` is at most 31 characters: a 12-hex hash of the rendered PNG plus a Unix timestamp (`3fa9c2e81b07-1792087200`). Longer names are truncated by the firmware; names without a 10-digit timestamp suffix are purged from its cache.
- Never return JSON `status: 500` from `/api/display`: firmware treats it as a reset and wipes its credentials.
- When the service is down, devices retry every 5 minutes until it is back. Accepted.

### Health

Recorded per request: battery voltage (also as a series), charging (`Battery-Charging`), USB connected (`USB-Connected`), RSSI, firmware version, wake reason, last seen. Battery voltage converts to a percentage through a resting-voltage table per model (the firmware measures before Wi-Fi and the display draw current).

Health reaches people through the screens: every page gets a `system` prop with this device's battery and stale modules, plus the other devices' last seen and battery. A unit that runs flat shows up on the other one.

## Rendering

### Page files

Pages are React components in TSX, styled with CSS modules, rendered by Takumi through `takumi-js` (no browser). A page exports the data it needs and a default component that receives the results as props:

```tsx
// pages/overview.tsx
import { query, type PageProps } from "./lib/page";
import { Screen } from "./components/Screen";
import { AreaChart } from "./components/AreaChart";
import { EventList } from "./components/EventList";
import styles from "./overview.module.css";

export const data = {
  weather: query("weather.forecast", { days: 3 }),
  power:   query("p1.power", { range: "24h", step: "5m" }),
  solar:   query("solar.production", { range: "24h", step: "5m" }),
  events:  query("calendar.upcoming", { limit: 5 }),
};

type Data = typeof data;

export default function Overview({ data, device, viewport, system, now }: PageProps<Data>) {
  return (
    <Screen device={device} system={system}>
      <div className={styles.grid}>
        <AreaChart series={[data.power, data.solar]} width={viewport.width} height={200} />
        <EventList result={data.events} now={now} />
      </div>
    </Screen>
  );
}
```

- `data` is static: `query()` only builds a descriptor (`{module, query, params}`). Components never fetch; everything arrives as props.
- Each `data.<key>` is a result envelope: `{value, fetchedAt, stale, error}`. Components decide how to show stale or failed data; shared components (`Screen`, cards) give a default.
- `PageProps<Data>` types each key from the generated query types.
- CSS modules: `import styles from "./x.module.css"`; Bun's bundler scopes class names and emits one CSS file. Takumi supports a CSS subset: class selectors, flexbox and grid, borders, fonts. Complex selectors and some properties may not render.
- Inline `<svg>` works (verified): charts are components that emit SVG.

### Queries

Modules expose named, typed queries; this is how stored data reaches pages.

```go
type powerParams struct {
	Range time.Duration `yaml:"range"`
	Step  time.Duration `yaml:"step"`
}

var _ = modules.Register[module, config](
	"p1",
	defaultConfig,
	modules.WithQuery("power", (*module).power),
)

func (m *module) power(ctx context.Context, p powerParams) (Series, error) {
	// series are service-owned; the service picks the rollup and buckets
	return modules.Series(ctx).Query(ctx, "p1.power_import", p.Range, p.Step)
}
```

- Params are decoded from the page header into the query's own struct; unknown keys and bad values fail at startup, not at render.
- Results are plain structs that serialize to JSON; TypeScript types for them are generated (see [Types](#types)).
- Queries read storage only, never the source: their own tables through sqlc on the read-only connection, series through `modules.Series(ctx)`.

### Data volume

24 hours of one-minute samples is 1,440 points per series; P1 import, P1 export and solar together are ~4,300. The E1001 is 800 pixels wide, so more points than pixels add nothing. Series queries take a `step` and bucket in SQL (average for power, last value for meter readings): 5 minutes gives 288 points per series. Rollup tables serve longer ranges. Chart components bucket again when given more points than their pixel width.

### Renderer process

A long-running Bun process (`renderer/`), started and supervised by the Go service. Per-render process start would cost 50-100 ms; a resident process renders in ~20 ms (measured, 800x480).

- **Bundling**: at start the renderer bundles `pages/` (shipped in the image) with `Bun.build` into JS plus CSS. With `watch` on, for development, it rebuilds on file changes. Pages change through a new image; no pages on the config mount in v1.
- **Protocol**: length-prefixed frames over stdin/stdout, one request at a time. Each frame is a 4-byte big-endian length, then a JSON header, then for render responses the raw PNG bytes (header carries the PNG length). No base64, no line-length limits. Covered by tests on both sides.

| Request | Response |
| --- | --- |
| `{"id":1,"op":"describe"}` | every page with its `data` descriptors and bundle hash |
| `{"id":2,"op":"render","page":"overview","viewport":{...},"props":{...}}` | header `{"id":2,"png":<bytes>}` + PNG, or `{"id":2,"error":"..."}` |

- **Logging**: stdout is reserved for frames; the renderer redirects `console.log` to stderr at start, and Go logs stderr lines.
- **Supervision**: Go restarts the process on exit with backoff and applies a timeout per render. A request in flight when the process dies fails; the device keeps its last image and gets it again on the next wake.

### Pipeline

1. **Describe** (startup and after a rebuild): the renderer reports pages and their `data`. Go checks that each `module.query` exists and its params decode into the query's params type. Errors stop startup; after a rebuild, they keep the previous bundle. Durations in params use a `modules.Duration` type that decodes `"24h"`-style strings (plain `time.Duration` does not decode from JSON strings); its TypeScript type is a template literal string.
2. **Resolve**: Go runs the page's queries concurrently with a timeout. One failing query yields an error envelope, not a failed page.
3. **Render**: Go sends props to the renderer, gets an RGBA PNG.
4. **Reduce** (Go): rotate if needed, map each pixel to the nearest of 4 gray levels (0, 85, 170, 255) without dithering, which keeps text sharp, and encode a 2-bit PNG. Both models use 4 levels.
5. **Check**: the PNG must be under 750 KB (both models; firmware limit with PSRAM). A 2-bit image without dithering is far below that (spike: 34 KB at 1872x1404); exceeding it is logged and the previous image is served.
6. **Name**: `filename` is the PNG's hash plus timestamp (see [Protocol](#protocol)). An unchanged image keeps its filename and the device skips the download. Props include `now` and battery values, so most wakes produce a new image; that is accepted.

Per-device params: a device can override a page's query params in config (e.g. `limit` for the bigger E1003), merged over the page's declared params before resolving.

### Types

Go query types are the source of truth. `cmd/gentypes` walks every registered query's params and result types by reflection and writes `pages/generated/queries.ts`: one interface per struct (named module + Go type name), and a `Queries` map from `"module.query"` to `{params, result}`. `query()` and `PageProps` use it, so a typo in a query name, a wrong param or a wrong field is a type error in the editor and in CI (`tsc --noEmit`). The generated file is committed; CI checks it is up to date.

Mapping rules: `time.Time` is a string (RFC 3339), `modules.Duration` is `Duration` (a string like `"24h"`), pointers are `T | null`, `[]byte` is a string, maps have string keys. Slices and maps are typed without `null`, so queries return non-nil ones. Params fields are all optional: a missing param is the Go zero value, which a query treats as its default.

Pages declare `type Data = typeof data` and use `PageProps<Data>`: destructuring `data` from props would otherwise shadow the module's `data` in its own type.

### Shared code

- `pages/lib/`: `query`, `PageProps`, formatting (Dutch decimal comma, day and month names, durations, "updated at" labels). Pure functions.

Time: the config's `timezone` is the single source. Go loads it with `time/tzdata` embedded, computes everything in it, and passes `now` and all timestamps to pages as ISO 8601 strings with offset, plus `timezone` in props. Formatting uses `Intl` with `nl-NL`; a `bun test` asserts the image's ICU data formats Dutch dates as expected.
- `pages/components/`: `Screen` (frame with device status from `.system`), cards, charts (`LineChart`, `AreaChart`, `BarChart`, `Sparkline` as inline SVG), event list.

### Testing

Each step has its own test with no network:

| What | Tool | How |
| --- | --- | --- |
| Module `Update` | `go test` | `httptest` fake of the source, assert on stored state and series |
| Queries | `go test` | storage in a temp dir with migrations applied, seeded with inserts, `cmp.Diff` on the returned struct |
| Migrations | `go test` | all owners' migrations applied together to one empty database, so table name collisions show |
| Pipeline | `go test` | fake renderer and queries, including failing and stale ones |
| Formatting, chart math | `bun test` | table tests on pure functions |
| Pages | `bun test` | fixture props (`pages/testdata/<page>.json`) rendered, reduced to 4 grays, compared to a golden PNG with a small pixel tolerance; a mismatch writes a diff image. Goldens are regenerated deliberately on Takumi or font upgrades. |
| Types | `tsc --noEmit` | pages against generated types |

Fixtures come from the live service: the preview serves the resolved props as JSON (`/preview/{device}/{page}/props.json`), saved as-is into `pages/testdata/`.

### Fonts

Takumi's built-in sans-serif, a variable font covering all weights. No font files are loaded.

## Web UI

On a separate admin listener. No authentication; access is restricted at the network level (bind addresses in config, TrueNAS port mapping, firewall rules).

| Route | Purpose |
| --- | --- |
| `GET /` | Devices, pages, module status (last update, errors), pending devices |
| `GET /preview/{device}/{page}.png` | Exactly what the device gets |
| `GET /preview/{device}/{page}/props.json` | The props the page received; saved as test fixtures |
| `GET /preview/page/{page}.png?model=...&rotation=...` | A page on a panel without a device |
| `GET /queries` | Every registered query with its params and a sample result |
| `GET /healthz` | Liveness |

## Configuration

One YAML file on a mount. `${VAR}` references are substituted from the environment, so secrets can be env vars or inline values.

```yaml
listen:
  device: "0.0.0.0:8080"
  admin: "192.168.1.10:8081"   # bind to the trusted interface only
public_url: http://192.168.1.10:8080   # base for image_url, reachable from the devices
timezone: Europe/Amsterdam
data_dir: /data

schedule:
  first_wake: "07:00"
  last_wake: "00:00"
  interval: 1h
  detail_timeout: 5m

modules:
  weather:
    location: "Amsterdam, The Netherlands"   # geocoded once, coordinates cached in the module's table
  p1:
    url: http://192.168.1.20
  calendar:
    ical_url: ${CALENDAR_ICAL_URL}
  car:
    client_id: ${PLEOS_CLIENT_ID}
    client_secret: ${PLEOS_CLIENT_SECRET}

devices:
  hallway:
    mac: "AA:BB:CC:DD:EE:FF"
    rotation: 0
    pages: [overview, energy, car]
    params:                       # optional per-device query param overrides
      overview.events: {limit: 8}
```

Runtime state (device API keys, module caches) lives in the database, not the config file.

## Deployment

- One Docker image, multi-stage: Go service, Bun runtime with the renderer and `node_modules`, pages. Base is a slim image with Bun (~60-90 MB); the Go binary is static.
- CI: `go test`, `bun test`, `tsc --noEmit`, generated types and sqlc code up to date.
- Built by GitHub Actions, pushed to GHCR.
- Runs on TrueNAS with `/config` (YAML) and `/data` (SQLite, own ZFS dataset, `recordsize=16K`) mounts.
- Restart policy `unless-stopped` and a Docker health check on `/healthz`.
- Backup: ZFS snapshots of the `/data` dataset.

## Open questions

1. To verify on hardware: `Update-Source` values for a button wake, which button wakes each model, firmware rotation direction, resting-voltage table for the battery percentage.
2. Takumi CSS coverage: find limits as pages are built.
3. Write load: measure actual bytes written per day once running, and compare with the estimate in [Write load](#write-load).
