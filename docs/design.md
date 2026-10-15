# Design: dashboard service

Draft, 2026-10-15. Background and data source research: [research.md](research.md).

## Overview

One Go service, `dashboard`, serves two Seeed reTerminal e-ink units (E1001, E1003) running TRMNL firmware in BYOS mode. The service owns I/O, storage, scheduling, rendering and the device protocol. Data sources are modules: Go packages that are registered at compile time, configured in YAML, and global to the system (not tied to a device).

```
             +---------------------- dashboard ----------------------+
             |                                                       |
 P1 dongle --+--> p1 ------+                                         |
 S-Miles   --+--> solar ---+                                         |
 Google    --+--> calendar +--> storage --> pages --> render --> device API <-- E1001
 Pleos     --+--> car -----+   (state +     (templates)  (Takumi)      |      <-- E1003
 Open-Meteo -+--> weather -+    series)                               |
             |                    ^                                   |
             |   scheduler -------+          web UI: preview, module routes (OAuth)
             +-------------------------------------------------------+
```

## Modules

A module is a package that collects data from one source and exposes it to templates.

Initial modules:

| Module | Source | Kind of data |
| --- | --- | --- |
| `p1` | Smartstuff P1 dongle, local HTTP | current state + time series (import/export power, energy, gas) |
| `solar` | Hoymiles: S-Miles Cloud now, DTU Modbus later | current state + time series (production per inverter, alarms) |
| `calendar` | Google Calendar secret iCal URL | current state (upcoming events) |
| `car` | Pleos Vehicle Data API, fallback Bluelink | current state (SoC, range, charging, locks) |
| `weather` | Open-Meteo | current state (now, hourly, daily) |

Possible later: Plex recently added, waste collection, public transport.

Device health is a core concern, not a module (see [Devices](#devices)).

### Contract

What the core needs from a module:

1. **Name**: stable identifier, used as config key, storage namespace and template key.
2. **Config**: decoded from `modules.<name>` in the config file. Unknown keys are an error.
3. **Collect**: called by the scheduler; fetches from the source and writes to storage.
4. **View**: returns the template data for the module, built from storage. Pure read, no network.

Optional:

5. **Routes**: HTTP handlers mounted under `/modules/<name>/` on the admin listener. For auth flows (OAuth redirect/callback, login forms) and module-specific debug pages.
6. **Backfill**: one-time import of history on first start (P1 dongle keeps hours/days/months locally).

Modules are registered explicitly in `main`; disabled modules are not constructed. Each module gets its dependencies injected: its config, a storage handle scoped to its namespace, an HTTP client with timeouts, a logger, a clock.

Collect and View are split so a slow or failing source never blocks rendering: a page always renders from the last stored data.

## Storage

One SQLite database on the data volume, using a pure-Go driver (`modernc.org/sqlite`) so the build stays free of cgo.

### State

Latest result per module: a JSON document plus metadata.

| Field | Purpose |
| --- | --- |
| `module` | namespace |
| `data` | JSON of the module's state |
| `fetched_at` | time of the last successful collect |
| `attempted_at`, `error` | last attempt and its error, if any |

Templates get `fetched_at` and a staleness flag (age above the module's `stale_after`) next to the data.

### Time series

Needed for P1 and solar history graphs, and device health. Volume is small: P1 at one sample per 10 s is ~3 M rows a year raw; solar per inverter port per minute ~10 M.

Options:

| Option | For | Against |
| --- | --- | --- |
| **SQLite tables** with raw samples + rollups (5 min, 1 h, 1 d) and retention | one file, one backup, no extra container, SQL for queries | rollups and retention are ours to write |
| Embedded TSDB library (e.g. `tstorage`) | purpose-built, compression | another storage engine next to SQLite, smaller communities |
| External TSDB container (VictoriaMetrics, InfluxDB) | mature, fast, own UI | extra container to run and back up; overkill for two graphs |

Proposed: SQLite. Schema `series(name, ts, value)` with rollup tables per resolution; raw kept 7 days, 5-minute 90 days, hourly and daily forever. Graph queries pick the coarsest resolution that still fills the graph's pixel width.

Counters (energy kWh, gas m³) are stored as meter readings; usage per period is the difference. Rates (W) are stored as samples and averaged in rollups.

## Scheduler

- Each module has a collect interval (module default, overridable in config) and a `stale_after`.
- Jitter on start, exponential backoff on errors, capped at the interval.
- Optional active hours per module (e.g. car only 06:00-23:00, to spare the Bluelink quota).
- Rollup and retention jobs run on the same scheduler.

## Devices

### Configuration

Per device: name, MAC address, model (`seeed_e1001`, `seeed_e1003`), rotation, pages, schedule.

```yaml
devices:
  hallway:
    mac: "AA:BB:CC:DD:EE:FF"
    model: seeed_e1001
    pages: [overview, energy]
    schedule:
      active: "06:30-23:00"
      refresh: 15m
```

### Protocol

TRMNL BYOS, on the device listener:

| Route | Behavior |
| --- | --- |
| `GET /api/setup` | Known MAC: issue and store an API key. Unknown MAC: record it as pending (shown in the web UI), return a non-200 so the device retries. |
| `GET /api/display` | Authenticate by `Access-Token`. Record health headers. Pick the page, render if inputs changed, return `image_url`, `filename` (content hash), `refresh_rate`. |
| `GET /images/{hash}.png` | Rendered image. |
| `POST /api/log` | Store device log entries. |

Never return JSON `status: 500` from `/api/display`: firmware treats it as a reset and wipes its credentials.

`refresh_rate` comes from the schedule: the refresh interval inside active hours, otherwise the seconds until the next active window starts.

### Pages

A device cycles through its pages. The firmware sends `Update-Source` with the wake reason: a button wake advances to the next page, a timer wake shows the first page. To verify on hardware: which buttons wake the unit under TRMNL firmware, and whether the E1003's page up/down buttons can be told apart.

### Health

Recorded from every `/api/display` request: battery voltage, charging, RSSI, firmware version, wake reason, last seen. Battery voltage is stored as a time series and converted to a percentage per model.

Health reaches people through the screens: every page gets a `.System` block with this device's battery, stale modules and errors, and the other devices' status (last seen, battery). A unit that runs flat shows up on the other unit.

## Rendering

- Templates: Go `html/template`, one per page, plus shared partials. Layout lives entirely in HTML/CSS; design is a separate track.
- Template data: `.Modules.<name>` (view + freshness), `.Device`, `.System`, `.Now`, formatting helpers (Dutch locale: decimal comma, day/month names, Europe/Amsterdam).
- Graphs: rendered as inline SVG by template helpers from series queries.
- Renderer: Takumi through a small Rust CLI run as a subprocess (validated in the spike, commit `8da478f`; 12-35 ms per render). Go reduces the output to the panel's gray levels (4 or 16) and encodes a 2- or 4-bit PNG. cgo binding only if subprocess overhead ever matters.
- A render's identity is the hash of page, template version and the data it used; same hash means no re-render and no device redraw.
- Fonts are bundled in the image.

## Web UI

On a separate admin listener, so the device listener can be the only port exposed to the IoT network.

| Route | Purpose |
| --- | --- |
| `GET /` | Devices, pages, module status (last fetch, errors), pending devices |
| `GET /preview/{device}/{page}.png` | Exactly what the device would get |
| `GET /preview/{device}/{page}.html` | The HTML fed to the renderer, for debugging in a browser (browser rendering may differ from Takumi) |
| `GET /preview/page/{page}.png?model=...` | Page on a given panel without a device |
| `/modules/{name}/...` | Module routes, e.g. `/modules/car/login` |
| `GET /healthz` | Liveness |

No authentication in v1; the admin listener is only reachable from the trusted network.

## Configuration

One YAML file on a mount. `${VAR}` references are substituted from the environment, so secrets can come from env vars or be written inline.

```yaml
listen:
  device: ":8080"
  admin: ":8081"
timezone: Europe/Amsterdam
data_dir: /data

modules:
  weather:
    latitude: 52.37
    longitude: 4.89   # Amsterdam
  p1:
    url: http://192.168.1.20
  calendar:
    ical_url: ${CALENDAR_ICAL_URL}
  car:
    client_id: ${PLEOS_CLIENT_ID}
    client_secret: ${PLEOS_CLIENT_SECRET}
```

Module state that changes at runtime (OAuth tokens, device API keys) lives in the database, not the config file.

## Package layout

```
main.go          wiring: config, storage, modules, scheduler, listeners
config/          load YAML, env substitution, validation
storage/         SQLite: state, series, rollups, devices
scheduler/       intervals, backoff, active hours
device/          TRMNL protocol, health, page selection
render/          templates, Takumi subprocess, gray reduction, PNG
web/             admin UI, preview, module route mounting
modules: p1/ solar/ calendar/ car/ weather/
renderer/        Rust CLI around Takumi
templates/       pages, partials, CSS
fonts/
```

Modules depend on small interfaces they define themselves (for storage, clock), not on core packages, so `main` stays the only wiring point.

## Deployment

- One Docker image, multi-stage: Rust renderer, Go service, fonts and templates on a distroless base.
- Built by GitHub Actions, pushed to GHCR.
- Runs on TrueNAS with `/config` (YAML) and `/data` (SQLite) mounts.
- Backup: the SQLite file (online backup API or `VACUUM INTO`).

## Open questions

1. Time series: confirm SQLite with rollups, and the retention periods.
2. Pages: auto-cycle on timer wakes, or always first page?
3. Templates: embedded in the binary (immutable image) or loaded from the config mount (edit without rebuild)?
4. Button behavior per model, to verify on hardware.
5. E1003 mounting orientation.
