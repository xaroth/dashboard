# Design: dashboard service

Draft, 2026-10-15. Background and data source research: [research.md](research.md).

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
             |          ^                                +--> web UI: preview, module routes (OAuth)
             |      scheduler                                                       |
             +--------------------------------------------------------------------+
```

## Code layout

```
cmd/dashboard/main.go      entrypoint: load config, build service, run
cmd/dashboard/config.go    config loading: YAML file, ${ENV} substitution, validation
service/service.go         the service: wires storage, scheduler, modules, listeners
service/modules/           registration API and the module contract
service/storage/           SQLite: state, series, rollups, devices
service/scheduler/         module updates, rollups, retention
service/device/            TRMNL protocol, health, page selection, screen schedule
service/render/            page pipeline, renderer process, gray reduction, PNG
service/web/               admin UI, preview, module route mounting
modules/modules.go         blank-imports every module so it registers
modules/<name>/            one module per data source
renderer/                  Bun process: bundles pages, renders with takumi-js
pages/                     TSX pages, components, CSS modules
pages/generated/           TypeScript types generated from Go query results
fonts/
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

// Initialize geocodes Location through Open-Meteo once and caches the
// coordinates in state; a changed Location triggers a new lookup.

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

func (m *module) Update(ctx context.Context) error {
	db := modules.Database(ctx)
	// fetch from the source, write state and series
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

Required:

| Method | Purpose |
| --- | --- |
| `Initialize(ctx, *C) error` | Receive config (defaults overlaid with `modules.<name>` from YAML; unknown keys are an error). Set up clients, run backfill if storage is empty. |
| `Update(ctx) error` | Called by the scheduler: fetch from the source, write to storage. |

Optional, detected by interface assertion:

| Method | Purpose |
| --- | --- |
| `Routes(mux *http.ServeMux)` | Handlers mounted under `/modules/<name>/` on the admin listener: OAuth redirect/callback, login forms, debug pages. |

Options: `WithUpdateInterval`, `WithStaleAfter`, `WithActiveHours` (e.g. car only 06:00-23:00 to spare the Bluelink quota), and `WithQuery` to expose data to pages (see [Queries](#queries)). Config can override the timing options.

Update and queries are split so a slow or failing source never blocks a screen: pages render from the last stored data.

### Dependencies

Dependencies come through the context: `modules.Database(ctx)`, `modules.Logger(ctx)`, `modules.HTTPClient(ctx)`, `modules.Clock(ctx)`. The service puts them on the context it passes to `Initialize`, `Update` and query functions. Tests build a context with fakes through the same setters.

## Storage

One SQLite database on the data volume, pure-Go driver (`modernc.org/sqlite`), no cgo.

### Keeping it in memory

Two ways to keep most data in memory with periodic disk writes:

1. **On-disk SQLite tuned for memory** (chosen): WAL mode, `synchronous=NORMAL`, a large page cache and `mmap_size`, so reads are served from memory. Writes are buffered in the service (e.g. P1 samples) and flushed in one transaction per minute. A crash loses at most the unflushed buffer; the file is always consistent.
2. **In-memory database** with a periodic snapshot to disk (`VACUUM INTO` or the backup API). Fastest, but a crash loses everything since the last snapshot, and startup loads the snapshot back.

Data volume either way: tens of MB per year.

### State

Latest result per module: a JSON document plus `fetched_at`, `attempted_at` and `error`. Queries return the data with its freshness (age above `stale_after` marks it stale).

### Time series

For P1 and solar history graphs and device battery. Schema `series(name, ts, value)` with rollup tables at 5 minutes, 1 hour and 1 day. Retention: raw 7 days, 5-minute 90 days, hourly and daily forever. Queries pick the coarsest resolution that still fills the graph.

Counters (kWh, m³) are stored as meter readings; usage per period is the difference. Rates (W) are averaged in rollups.

## Scheduler

- Module updates: per-module interval, jitter on start, exponential backoff on errors capped at the interval, optional active hours.
- Storage jobs: flush buffered writes, rollups, retention.

## Devices

### Configuration

```yaml
devices:
  hallway:
    mac: "AA:BB:CC:DD:EE:FF"
    model: seeed_e1001
    rotation: 0          # 0, 90, 180, 270
    pages: [overview, energy, car]
```

### Screen schedule

Global, overridable per device: active 07:00-01:00, refresh every hour on the hour.

- **Timer wake**: show the first page. `refresh_rate` is the seconds until the next full hour; after 01:00, until 07:00.
- **Button wake**: refresh and advance to the next page, wrapping around. `refresh_rate` stays aimed at the next full hour, so the hourly wake returns to the first page. Pages after the first are detail pages.
- The ESP32 clock drifts during deep sleep; computing `refresh_rate` from the server clock on every wake corrects it.

Battery use: 18 timer wakes a day plus button presses.

### Buttons

Every `/api/display` request carries `Update-Source` with the wake reason: `timer`, a GPIO source (`EXT0`, `EXT1`, `gpio`) for a button, `powercycle` after power-on. The firmware does not say which button: any button means "next page". Double-click runs a special function (signaled with a `special_function` header) and a 5-second hold resets Wi-Fi, so those stay out of page logic. To verify on hardware: which buttons wake each model (E1001: green button; E1003: refresh, and whether page up/down wake it) and the exact `Update-Source` values.

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
| `GET /api/setup` | Known MAC: issue and store an API key. Unknown MAC: record as pending (shown in the web UI), return non-200 so the device retries. |
| `GET /api/display` | Authenticate by `Access-Token`, record health headers, pick the page, render if needed, return `image_url`, `filename` (content hash), `refresh_rate`. |
| `GET /images/{hash}.png` | Rendered image. |
| `POST /api/log` | Store device log entries. |

Never return JSON `status: 500` from `/api/display`: firmware treats it as a reset and wipes its credentials.

### Health

Recorded per request: battery voltage (also as a series), charging, RSSI, firmware version, wake reason, last seen. Battery voltage converts to a percentage per model.

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

export default function Overview({ data, device, viewport, system, now }: PageProps<typeof data>) {
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
- `PageProps<typeof data>` types each key from the generated query types.
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
	db := modules.Database(ctx)
	// SELECT bucketed averages over p.Range at p.Step
}
```

- Params are decoded from the page header into the query's own struct; unknown keys and bad values fail at startup, not at render.
- Results are plain structs that serialize to JSON; TypeScript types for them are generated (see [Types](#types)).
- Queries read storage only, never the source.

### Data volume

24 hours of one-minute samples is 1,440 points per series; P1 import, P1 export and solar together are ~4,300. The E1001 is 800 pixels wide, so more points than pixels add nothing. Series queries take a `step` and bucket in SQL (average for power, last value for meter readings): 5 minutes gives 288 points per series. Rollup tables serve longer ranges. Chart components bucket again when given more points than their pixel width.

### Renderer process

A long-running Bun process (`renderer/`), started and supervised by the Go service. Per-render process start would cost 50-100 ms plus font loading; a resident process renders in ~20 ms (measured, 800x480).

- **Bundling**: at start the renderer bundles `pages/` with `Bun.build` into JS plus CSS. The directory defaults to the copy in the image; `pages_dir` can point it to the config mount, so pages change without rebuilding the image. With `watch` on (preview use), it rebuilds on file changes.
- **Protocol**: newline-delimited JSON over stdin/stdout, one request at a time.

| Request | Response |
| --- | --- |
| `{"id":1,"op":"describe"}` | every page with its `data` descriptors and bundle hash |
| `{"id":2,"op":"render","page":"overview","viewport":{...},"props":{...}}` | `{"id":2,"png":"<base64>"}` or `{"id":2,"error":"..."}` |

- **Supervision**: Go restarts the process on exit with backoff, applies a timeout per render, and logs its stderr. A failed render keeps the device on its last image.
- **Fonts**: loaded once at start.

### Pipeline

1. **Describe** (startup and after a rebuild): the renderer reports pages and their `data`. Go checks that each `module.query` exists and its params decode into the query's params type. Errors stop startup; after a rebuild, they keep the previous bundle.
2. **Resolve**: Go runs the page's queries concurrently with a timeout. One failing query yields an error envelope, not a failed page.
3. **Key**: hash of bundle hash, page, viewport and props JSON. Same as last time for this device and page: reuse the PNG, the device skips the redraw.
4. **Render**: Go sends props to the renderer, gets an RGBA PNG.
5. **Reduce** (Go): rotate if needed, map to 4 or 16 grays, encode a 2- or 4-bit PNG.

### Types

Go query result types are the source of truth. A generator (e.g. `tygo`) writes them to `pages/generated/` as TypeScript, together with the query registry (`"p1.power" -> Series`, params type). `query()` and `PageProps` use it, so a typo in a query name or a wrong field is a type error in the editor and in CI (`tsc --noEmit`). Generated files are committed; CI checks they are up to date.

### Shared code

- `pages/lib/`: `query`, `PageProps`, formatting (Dutch decimal comma, day and month names, Europe/Amsterdam, durations, "updated at" labels). Pure functions.
- `pages/components/`: `Screen` (frame with device status from `.system`), cards, charts (`LineChart`, `AreaChart`, `BarChart`, `Sparkline` as inline SVG), event list.

### Testing

Each step has its own test with no network:

| What | Tool | How |
| --- | --- | --- |
| Module `Update` | `go test` | `httptest` fake of the source, assert on stored state and series |
| Queries | `go test` | seeded storage, `cmp.Diff` on the returned struct |
| Pipeline | `go test` | fake renderer and queries, including failing and stale ones |
| Formatting, chart math | `bun test` | table tests on pure functions |
| Pages | `bun test` | fixture props (`pages/testdata/<page>.json`) rendered and compared to a golden PNG; a mismatch writes a diff image |
| Types | `tsc --noEmit` | pages against generated types |

Fixtures come from the live service: the preview serves the resolved props as JSON (`/preview/{device}/{page}/props.json`), saved as-is into `pages/testdata/`.

### Fonts

Bundled in the image, loaded by the renderer.

## Web UI

On a separate admin listener, so only the device listener faces the IoT network. No authentication in v1.

| Route | Purpose |
| --- | --- |
| `GET /` | Devices, pages, module status (last update, errors), pending devices |
| `GET /preview/{device}/{page}.png` | Exactly what the device gets |
| `GET /preview/{device}/{page}/props.json` | The props the page received; saved as test fixtures |
| `GET /preview/page/{page}.png?model=...&rotation=...` | A page on a panel without a device |
| `GET /queries` | Every registered query with its params and a sample result |
| `/modules/{name}/...` | Module routes, e.g. `/modules/car/login` |
| `GET /healthz` | Liveness |

## Configuration

One YAML file on a mount. `${VAR}` references are substituted from the environment, so secrets can be env vars or inline values.

```yaml
listen:
  device: ":8080"
  admin: ":8081"
timezone: Europe/Amsterdam
data_dir: /data
pages_dir: /config/pages   # optional; defaults to the pages in the image

schedule:
  active: "07:00-01:00"
  refresh: hourly

modules:
  weather:
    location: "Amsterdam, The Netherlands"   # geocoded once, coordinates cached in state
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
    model: seeed_e1001
    pages: [overview, energy, car]
```

Runtime state (OAuth tokens, device API keys) lives in the database, not the config file.

## Deployment

- One Docker image, multi-stage: Go service, Bun runtime with the renderer and `node_modules`, pages, fonts. Base is a slim image with Bun (~60-90 MB); the Go binary is static.
- CI: `go test`, `bun test`, `tsc --noEmit`, generated types up to date.
- Built by GitHub Actions, pushed to GHCR.
- Runs on TrueNAS with `/config` (YAML, optional pages) and `/data` (SQLite) mounts.
- Backup: SQLite online backup or `VACUUM INTO`.

## Open questions

1. Buttons and rotation direction: to verify on hardware.
2. Takumi CSS coverage: find limits as pages are built.
