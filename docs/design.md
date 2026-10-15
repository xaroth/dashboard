# Design: dashboard service

Draft, 2026-10-15. Background and data source research: [research.md](research.md).

## Overview

One Go service, `dashboard`, serves two Seeed reTerminal e-ink units (E1001, E1003) running TRMNL firmware in BYOS mode. The service owns I/O, storage, scheduling, rendering and the device protocol. Data sources are modules: packages under `modules/` that register themselves, are configured in YAML, and are global to the system (not tied to a device).

```
             +---------------------------- dashboard -----------------------------+
             |                                                                    |
 P1 dongle --+--> modules/p1 -------+                                             |
 S-Miles   --+--> modules/solar ----+                                             |
 Google    --+--> modules/calendar -+--> storage <-- pages (templates) --> render --+--> device API <-- E1001
 Pleos     --+--> modules/car ------+   (state +        |                  (Takumi) |               <-- E1003
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
service/render/            template pipeline, Takumi subprocess, gray reduction, PNG
service/web/               admin UI, preview, module route mounting
modules/modules.go         blank-imports every module so it registers
modules/<name>/            one module per data source
renderer/                  Rust CLI around Takumi
templates/                 pages, partials, CSS (embedded, overridable)
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
| `View(ctx) (any, error)` | Template-facing object for this module. Its methods are called from templates and read storage lazily (see [Rendering](#rendering)). Never touches the network. |
| `Routes(mux *http.ServeMux)` | Handlers mounted under `/modules/<name>/` on the admin listener: OAuth redirect/callback, login forms, debug pages. |

Options: `WithUpdateInterval`, `WithStaleAfter`, `WithActiveHours` (e.g. car only 06:00-23:00 to spare the Bluelink quota). Config can override each.

Update and View are split so a slow or failing source never blocks a screen: pages render from the last stored data.

### Dependencies

The proposal above passes dependencies through the context (`modules.Database(ctx)`, likewise logger, HTTP client, clock). Alternative: a typed `*modules.Host` argument to `Initialize` holding the same dependencies, kept on the module struct. It makes dependencies visible in signatures and avoids a runtime lookup that can panic when missing. To decide.

## Storage

One SQLite database on the data volume, pure-Go driver (`modernc.org/sqlite`), no cgo.

### Keeping it in memory

Two ways to keep most data in memory with periodic disk writes:

1. **On-disk SQLite tuned for memory** (proposed): WAL mode, `synchronous=NORMAL`, a large page cache and `mmap_size`, so reads are served from memory. Writes are buffered in the service (e.g. P1 samples) and flushed in one transaction per minute. A crash loses at most the unflushed buffer; the file is always consistent.
2. **In-memory database** with a periodic snapshot to disk (`VACUUM INTO` or the backup API). Fastest, but a crash loses everything since the last snapshot, and startup loads the snapshot back.

Data volume either way: tens of MB per year.

### State

Latest result per module: a JSON document plus `fetched_at`, `attempted_at` and `error`. Templates get the data plus freshness (age above `stale_after` marks it stale).

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

Templates get the resulting viewport (`.Viewport.Width`, `.Viewport.Height`, `.Viewport.Portrait`). The firmware's rotation direction is to confirm on hardware.

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

Health reaches people through the screens: every page gets `.System` with this device's battery and stale modules, plus the other devices' last seen and battery. A unit that runs flat shows up on the other one.

## Rendering

### Pipeline

1. **Select**: device, page and viewport from the request.
2. **Execute template**: Go `html/template` with root data `.Device`, `.Viewport`, `.System`, `.Now`. Module data is pulled during execution: `{{ with module "weather" }}{{ .Current.Temperature }}{{ end }}`. `module` returns the module's `View` object; its methods can take arguments and query storage on demand, e.g. `{{ (module "p1").Usage "24h" }}`.
3. **Hash**: hash the HTML output. Same hash as the last render for this device and page: reuse the PNG, the device skips the redraw.
4. **Render**: Takumi CLI (subprocess, spike in commit `8da478f`: 12-35 ms) turns HTML + CSS into a PNG at the viewport size.
5. **Reduce**: rotate if needed, map to the panel's gray levels (4 or 16), encode a 2- or 4-bit PNG.

Executing a template costs milliseconds, so hashing its output is the simplest correct cache key: anything that changes the screen changes the hash.

### Templates

- Embedded in the binary as defaults (`embed.FS`), overridable by a `templates_dir` on the config mount. The override wins per file, so one page can be edited without rebuilding the image. Preview requests reload templates from disk.
- Shared helpers: Dutch formatting (decimal comma, day and month names, Europe/Amsterdam), durations, freshness labels.
- Graphs: helpers that turn a series query into inline SVG (line, bar, sparkline). Takumi's support for inline `<svg>` versus SVG as `<img>` data URI is to verify.
- Fonts bundled in the image.

## Web UI

On a separate admin listener, so only the device listener faces the IoT network. No authentication in v1.

| Route | Purpose |
| --- | --- |
| `GET /` | Devices, pages, module status (last update, errors), pending devices |
| `GET /preview/{device}/{page}.png` | Exactly what the device gets |
| `GET /preview/{device}/{page}.html` | The HTML fed to the renderer (browser rendering may differ from Takumi) |
| `GET /preview/page/{page}.png?model=...&rotation=...` | A page on a panel without a device |
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
templates_dir: /config/templates   # optional overrides

schedule:
  active: "07:00-01:00"
  refresh: hourly

modules:
  weather:
    location: "Amsterdam, The Netherlands"
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

- One Docker image, multi-stage: Rust renderer, Go service, fonts and default templates, distroless base.
- Built by GitHub Actions, pushed to GHCR.
- Runs on TrueNAS with `/config` (YAML, template overrides) and `/data` (SQLite) mounts.
- Backup: SQLite online backup or `VACUUM INTO`.

## Open questions

1. Module dependencies: through the context, or a typed `*modules.Host` argument?
2. Storage: on-disk SQLite tuned for memory, or in-memory with snapshots?
3. Weather location as a place name needs geocoding (Open-Meteo has a free geocoding API), resolved once at `Initialize`. Or configure coordinates directly?
4. Buttons and rotation direction: to verify on hardware.
