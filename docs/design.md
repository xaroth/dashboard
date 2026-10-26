# Design: dashboard service

This document describes the service as built. Planned parts (more modules, device health, more components, page tests, deployment) are specified in [plan.md](plan.md). Background and data source research: [research.md](research.md).

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
cmd/gentypes/              generates pages/generated from the module query types and
                           the page props
storage/                   SQLite: connections, per-owner migrations
module/                    registration API, module contract, scheduler, query dispatch,
                           module state
module/sql/                module state migrations and queries
module/yamlconfig/         decodes the modules.<name> YAML sections for module.Load
device/                    TRMNL protocol, API keys, page selection, screen schedule,
                           in-memory image store
series/                    time series: write buffer, rollups, retention, bucketed queries
series/sql/                series migrations and queries
device/sql/                API key migrations and queries
render/                    page pipeline, renderer process, gray reduction, PNG
screen/                    what device and render share: render target, wake, image
modules/modules.go         modules.All: the list of every module
modules/<name>/            one module per data source
modules/<name>/sql/        the module's migrations and queries
modules/<group>/<name>/    a group's shared values and modules, each named
                           <group>_<name>: eveonline/esi (ESI client and characters),
                           eveonline/sde (static data), eveonline/status (a module)
module/moduletest/         helpers for module tests
renderer/                  Bun process: bundles pages, renders with takumi-js
pages/shared/              TSX pages and their CSS modules, for any layout
pages/layouts/<layout>/    TSX pages and their CSS modules, one folder per layout
pages/components/, lib/    shared by all pages
pages/generated/           TypeScript types generated from Go query results and props
tools/                     standalone scripts (dtu_read.py: read-only Modbus check)
```

Packages are one level deep and named for what they do. They do not import each other sideways; `cmd/dashboard` wires them together:

```
cmd/dashboard  -> storage, module, module/yamlconfig, modules, device, render
module         -> series
module/yamlconfig -> module
cmd/gentypes   -> module, modules, render, screen
modules        -> module, modules/<name>
modules/<name> -> module
modules/<group>/<name> -> module, the group's shared values
device         -> screen
render         -> screen
```

`module`, `series` and `device` do not import `storage`: `module.Load`, `series.Open` and `device.Load` take their connections and a `Migrator`, which `*storage.DB` satisfies, and run their own migrations. `device` renders through its `Pipeline` interface, which `*render.Pipeline` satisfies.

## Modules

| Module | Source | Data |
| --- | --- | --- |
| `eveonline_skills` | ESI `/characters/{id}/skillqueue`, `/characters/{id}/skills` (logged-in characters); SDE types for skill names | snapshot (each character's queue, trained skills and skill points) in module state |
| `eveonline_status` | ESI `/status` (EVE Online, Tranquility) | snapshot (online, players, server version, start time) in module state; player count in a series while online |
| `p1` | Smartstuff P1 dongle (DSMR-API firmware), local HTTP `/api/v2/sm/actual` | snapshot (latest reading) in module state; first reading of each day in `p1_days`, first gas reading of each day in `p1_gas_days` |
| `solar` | Hoymiles DTU-Pro-S, Modbus TCP | snapshot (DTU serial and every inverter input) in module state |
| `calendar` | iCalendar (`.ics`) feeds by URL, e.g. Google Calendar's secret address in iCal format | snapshot (each calendar's events for the next 32 days, recurrences expanded) in module state |
| `rss` | RSS 1.0, RSS 2.0, Atom and JSON Feed URLs | snapshot (per feed: title, newest items, HTTP validators) in module state |
| `car` | Pleos Vehicle Data API (Hyundai, Kia, Genesis; private My Vehicle API keys) | snapshot (per vehicle: SoC, range, charging, locks, position, odometer) in module state; keys, listed vehicles and polling state in `car_keys` and `car_vehicles` |
| `weather` | Open-Meteo forecast and geocoding APIs | snapshot (current, hourly, daily) in module state; geocoded location cached in `weather_locations` |

Planned modules are listed in [plan.md](plan.md#modules).

### Weather

Config keys under `modules.weather`:

| Key | Default | Meaning |
| --- | --- | --- |
| `location` | `Amsterdam, The Netherlands` | Place name, optionally with its country after a comma. Empty disables the module. |
| `forecast_days` | `7` | Days fetched, 1 to 16. Out of range fails `Initialize`. |
| `model` | empty | Open-Meteo weather model, e.g. `knmi_seamless` for KNMI HARMONIE. Empty lets Open-Meteo pick per location. |
| `geocoding_url` | `https://geocoding-api.open-meteo.com/v1/search` | Geocoding endpoint |
| `forecast_url` | `https://api.open-meteo.com/v1/forecast` | Forecast endpoint |

`Update` runs every 15 minutes; data is stale after 2 hours. The location is geocoded on the first update and cached per `location` string, so a changed value is looked up again. Query `weather.forecast` takes `hours` (hourly entries from the current hour, default 24) and `days` (daily entries from today, default 3).

### P1

Reads the smart meter through a Smartstuff P1 dongle with DSMR-API firmware. Config keys under `modules.p1`:

| Key | Default | Meaning |
| --- | --- | --- |
| `url` | empty | The dongle's base URL, e.g. `http://192.168.1.20`. Empty disables the module. |

`Update` runs every minute; data is stale after 10 minutes. Each update reads `/api/v2/sm/actual` with a 10 s timeout (the dongle's Wi-Fi is weak), stores the reading as the snapshot, inserts it into `p1_days` when the meter's day has no row yet, and its gas into `p1_gas_days` when the gas meter's day has no row yet. HTTP 401 or 403 (Basic Auth set in the dongle) is `module.ErrAuth`. These fail the update: a response without a timestamp, meter readings or power (the dongle sends one before it has read a telegram), all four electricity readings zero, and a meter time more than 10 minutes off the clock (the dongle lost the meter and serves its last telegram, or the meter's clock is wrong).

- The day is the date of the meter's timestamp (`YYMMDDhhmmss` plus `W` or `S`, CET or CEST), so the module needs no time zone. Times in the result are converted from it.
- `p1_days` holds one row per day: the first reading that day. Day totals come from these rows, not from the dongle's history, and need one write a day. When the dongle is offline at midnight, the first reading after it starts the day, and usage in the gap counts for the new day.
- `p1_gas_days` holds one row per gas day: the first gas reading that day, keyed on the gas meter's date and time (`gas_delivered_timestamp`; the meter's when the dongle does not send it). A DSMR 4 gas meter reports hourly, so at midnight its reading is from 23:00 and belongs to the day before. After a dongle restart gas is missing until the gas meter sends a reading; the first reading with gas starts the gas day.

Query `p1.actual` takes no params and returns:

- `measuredAt`: the meter's time of the reading.
- `voltage`: `l1`, `l2`, `l3` in V; null for phases the meter does not have.
- `flowKw`: power delivered minus power returned; positive is taken from the net, negative is returned to it.
- `today`: from the first reading of the meter's current day to the latest. `yesterday`: from the first reading of the day before to the first of today. Each has `from` and `to` (the readings' times; `from` later than midnight means the period is partial), `takenKwh` and `returnedKwh` (both tariffs), and `gasM3` with `gasFrom` and `gasTo` (the gas meter's times; the meter reports gas every 5 minutes, DSMR 4 meters hourly, so gas lags `to`). Gas is null without a gas meter, when the gas counter went down, or when the gas meter has not read on a day yet: `today` and `yesterday` gas are null after midnight until the first gas reading of the day. A period is null when a day has no row, or when an electricity counter went down in it (meter replaced).

Each update also writes [series](#time-series) at the meter's time: `p1.flow` (kW, rate, as `flowKw`), and the counters `p1.taken` and `p1.returned` (kWh, both tariffs). `p1.gas` (m³, when reported) is written at the gas meter's time (`gas_delivered_timestamp`; the meter's time when the dongle does not send it), and a gas reading whose time has not advanced since the last update is skipped.

Query `p1.flow` takes [history params](#time-series) and returns the average flow in kW per step up to now, as series points.

### Solar

Reads Hoymiles micro inverters through the DTU's Modbus TCP interface (DTU-Pro, DTU-Pro-S). Register map and the plant's inverters: [research.md](research.md#solar-hoymiles-micro-inverters). Config keys under `modules.solar`:

| Key | Default | Meaning |
| --- | --- | --- |
| `host` | empty | The DTU's address. Empty disables the module. |
| `port` | `502` | Modbus TCP port |
| `unused` | empty | Inputs without a panel, as `serial:port`, e.g. `100000000687:1`. Left out of the snapshot and totals. |
| `night_interval` | `10m` | Update interval while no input produces; at least 1 minute, under 30 minutes |

`Update` runs every minute (more frequent polling is reported to crash DTUs) and every `night_interval` while every input reads 0 W; data is stale after 30 minutes. Each update opens one connection, reads the DTU serial (FC03 at `0x2000`) and then each input's 20 registers at `0x1000 + i*40` (unit ID 1) until an input with an all-zero serial, at most 99, and closes it. The whole read has a 30 s timeout: the DTU stops answering at times. An update reading no inputs fails.

- The client in `modbus.go` implements FC03 only. It takes the frame length from the MBAP header and ignores the PDU's byte count, which some DTUs send wrong.
- One panel per input: an input's power is its panel's.
- `link` drops to 0 at times while an input's counters keep updating; totals count every input regardless.

Query `solar.production` takes no params and returns `powerW` (the sum over all inputs) and `panels`: `inverter` (serial), `port` and `powerW` per input, in DTU order.

Each update also writes [series](#time-series) at the service's clock (the DTU reports no measurement time): `solar.power` (W, rate, the sum over all inputs) and `solar.total` (Wh, counter, the inputs' lifetime totals summed).

Query `solar.power` takes [history params](#time-series) and returns the average power in W per step up to now, as series points. At night updates come every `night_interval`, so steps shorter than it have gaps.

The DTU is dialed through `Update`'s `u.Dialer`. `solar.NewFake(tb, opts...)` (in `dtu_fake.go`) is a dialer for `module.Options` that serves FC03 at `solar.FakeHost` in-process over `net.Pipe`, so tests run inside a synctest bubble; `ExpectWrongByteCount`, `ExpectException` and `ExpectHang` make it send wrong byte counts, an exception, or hang.

### Calendar

Reads named iCalendar feeds. Google Calendar's "secret address in iCal format" is one, so no Google API or OAuth is needed. Config keys under `modules.calendar`:

| Key | Default | Meaning |
| --- | --- | --- |
| `calendars` | empty | Calendars by name (lowercase letters, digits, `-`, `_`), each with a `url` (`http`, `https`, or `webcal`, fetched as `https`) and optionally `muted: true`, for a calendar pages show less prominently. Empty disables the module. |

```yaml
modules:
  calendar:
    calendars:
      family: { url: "${CALENDAR_FAMILY_ICAL}" }
      work:   { url: "${CALENDAR_WORK_ICAL}", muted: true }
```

A feed URL is a credential: it comes from an environment variable and is never logged, stored or shown. Config errors name the calendar, not the URL; fetch errors drop it (Go's `*url.Error` quotes it); requests are made with `module.WithSecretURL`, which keeps them out of traces, since a client span records the full URL.

`Update` runs every 15 minutes; data is stale after 2 hours. It fetches every calendar at once (at most 32 MB each), and expands its events from the start of today to 32 days later, in the configured `timezone`. A calendar that fails keeps its previous events with the error; the update fails only when all do, with `module.ErrAuth` when every feed answered 401 or 403. An event that fails to parse is skipped and logged with the calendar's name.

- Parsing is `github.com/emersion/go-ical`, recurrence `github.com/teambition/rrule-go` (RFC 5545 RRULE with BYSETPOS, BYDAY offsets and the rest). `github.com/apognu/gocal` parses and expands in one, but its own RRULE code handles a subset (`BYDAY=1MO` and `BYMONTHDAY` are wrong or ignored) and its time zone mapping is global state.
- Expansion: DTSTART is the first occurrence; RDATE adds, EXDATE removes (lists, any zone, or a date for a whole day). An event with a RECURRENCE-ID replaces the master's occurrence at that time, wherever it moves; a cancelled one only removes it. `RANGE=THISANDFUTURE` is treated as a single override. Cancelled events are left out. One event expands to at most 100,000 occurrences from its start; more skips it.
- Times: a TZID is an IANA name, a name with a prefix (`/freeassociation.sourceforge.net/Europe/Amsterdam`), or one the feed's VTIMEZONE maps with `X-LIC-LOCATION`; anything else (Outlook's Windows names) and floating times are in the configured `timezone`. Recurrences keep the wall clock across DST.
- All-day events are dates: they start at midnight on the first day and end at midnight after the last, in the configured `timezone`. A missing DTEND, or one not after DTSTART, is one day; for a timed event it is no duration.

Query `calendar.upcoming` takes `calendars` (names; empty is every calendar, an unknown one is an error), `days` (from today, today included; default 7, at most 31) and `limit` (default 10, at most 100). It returns `events`, every event that has not ended and starts before the end of the last day, by start with all-day events first: `calendar`, `title`, `start`, `end`, `allDay`, `location`. `calendars` has each calendar's `name`, `muted`, `fetchedAt` and `error`.

Page (shared): `calendar`, the next 15 days from today as day cells, 5 by 3 in landscape and 3 by 5 in portrait (`calendar.upcoming` for 15 days, up to 100 events, and `weather.forecast` for 7).

- A cell's heading has its short day, black for today and gray for weekends, and the forecast's icon and maximum while the forecast covers the day.
- Under it the day's events (`dayEvents`): all-day events and timed ones that fill the day as gray bars first, square at the ends where they run on into the day before or after, in lighter text on the days after the first; then timed events with their start, or `tot` and their end on later days. Events of muted calendars are gray. Timed events that ended are left out, as `calendar.upcoming` returns.
- As many rows as the cell height fits (7 at 800x480); more show `+N meer` in the last row (`fit`).
- The header has the date range (left out in portrait) and the update time, with a "!" when a calendar failed.

`calendar.NewFake(tb, opts...)` serves a Google-style and an Exchange-style feed from `modules/calendar/testdata` at `FakeFamilyURL` and `FakeWorkURL`; `ExpectFeed` and `ExpectStatus` set others up, and it counts requests not marked with `module.WithSecretURL`.

### Car

Reads Hyundai, Kia and Genesis vehicles through Pleos with private My Vehicle API keys (Pleos Playground, account menu > My Vehicle Data API > brand tab; Europe only, no approval). A key covers the vehicles of one brand account (Bluelink, Kia Connect, Genesis Connect), is valid for 90 days after it is issued and cannot be extended: it is reissued in Playground and entered again. Config keys under `modules.car`:

| Key | Default | Meaning |
| --- | --- | --- |
| `api_url` | `https://api.pleos.ai` | Pleos API base URL |
| `active_interval` | `15m` | Poll interval while a car is in use |
| `max_interval` | `12h` | Longest poll interval when every car is parked |

The module is enabled once a key is entered.

Admin page `/car/`:

- Add or replace a key: brand, client ID, client secret and issue date (default today). Saving checks the key with Pleos by issuing a token and listing its vehicles, then stores the key and its vehicles in `car_keys` and `car_vehicles` in one transaction; a key Pleos rejects, or one listing no vehicles, is not saved. Saving a client ID that exists replaces it and clears its error. The issue date is a day in the process's local time zone (`TZ`), and the key expires at the start of the 90th day after it. Secrets are stored in plain text, like the EVE tokens.
- Per key: issue and expiry date and days left, with a warning from 10 days before expiry; the last rejection; its vehicles. Delete removes the key and its vehicles in one transaction.
- Per vehicle: a short name (1 to 32 of `a-z`, `0-9`, `-`, `_`; unique; empty clears it), by which pages select it.
- Besides checking a key on save, the page only changes the database, and shares nothing else with updates. A changed key is picked up by the next update; after every key was rejected and the module paused, Update now (linked after saving) resumes it. An update first removes vehicles whose key was deleted while it ran.

Updates:

- Auth: `POST /v1/auth/personal-token` exchanges a key for an access token, kept in memory per key. There is no refresh token; tokens are JWTs valid for 1 hour (observed, undocumented). A token is issued on first use and again when a data call rejects it (`40003`, `40004`, `4011`, HTTP 401), then the call is retried once. Every call carries the key's brand in the `Brand` header.
- Vehicles: a key's vehicles are fixed when it is issued. The first update after start, or after a key's secret or brand changed, lists them (`GET /v1/vehicles/consent`) and drops ones no longer listed. A data call answering no consent (`5006`) or unknown VIN (`4101`) fails the update and makes the next one list again.
- Each update reads `/driving`, `/locations`, `/powertrains` and `/status` per vehicle, and `/batteries` for types `EV`, `PE` and `UNKNOWN`. A section answering No Data (`4045`) is null in the snapshot: models report different subsets.
- Keys fail on their own; the vehicles of a failed key keep the data of the last update. A key Pleos rejects (400, 401, 403 or 404 from the token endpoint, or a fresh token rejected), or one listing no vehicles, is recorded on the key and shown on the page until the key is saved again; a rejected key past its expiry date is not tried again. Other failures of a key (upstream errors, timeouts, 429) are logged and retried with the next update. The update fails only when no key worked: with `module.ErrAuth` when every key was rejected, which pauses the module, otherwise with the errors, which back off.
- Polling: a car is active on a poll when its ignition is on, it is charging, it moved 200 m or more since the previous poll (GPS fixes of a parked car wander), or its odometer rose 1 km or more (for cars without a position). The first poll of a car counts as active. After each update, `NextUpdate` sets the delay to a quarter of the time since the most recently active car was last active, between `active_interval` and `max_interval`: 15 minutes while in use, 1 hour after 4 hours parked, 12 hours after 2 days. Position and odometer of the previous poll are kept in `car_vehicles`, so this survives restarts. Data is stale after 13 hours; with a `max_interval` of 13 hours or more, raise `schedule.stale_after` above it.
- Reads return the last state the car sent, not a live reading: every section carries the time the car sent it (`updatedAt` is the newest), and a sleeping car answers with data from when it went to sleep. Waking the car is undocumented and not observed.
- Observed on a Hyundai KONA Electric (2024, `SX2HV`): every section answers. `outsideTemperature`, tires and sunroof are null, and `engine.fuelLevel` is 0 rather than null; the module reports no fuel level for type `EV`. Measurements with a null value are null in the snapshot.

Queries:

- `car.vehicle` takes `id`, a short name (any case) or VIN; empty is the first vehicle. A device selects its car by overriding `id` for the page. An unknown `id` is an error.
- `car.vehicles` takes no params and returns every vehicle, named ones first by short name, then by VIN.

A vehicle has: `vin`, `shortName`, `brand`, `nickname` (from the brand's app, often empty), `keyExpiresAt`, `model` (sales name), `modelCode`, `type`, `updatedAt`, `activeAt`, `ignitionOn`, `odometerKm`, `location`, `stateOfCharge`, `fuelLevel`, `ranges` (km per energy source), `charge` (plugged in, charging, status, AC/DC targets, minutes remaining), `locked` (all doors locked), `open` (doors, hood, trunk, sunroof), `climateOn`, `outsideTemperature` (°C) and `warnings`. Fields a car does not report are null. Short names and key expiry are read at query time, so changes on the admin page show without an update.

Pages (shared):

- `car`: one car from `car.vehicle`, the first unless the device sets `params: {car.car: {id: <short name or VIN>}}`. Params are per device, so a device shows one car this way; `cars` shows them all. Battery and tank level with a bar (battery for EV and PE, tank when reported; a hybrid's battery is left out), the longest range (a plug-in hybrid's total) with the others below, charge status (target from `targetDC` while fast charging, else `targetAC`; "vol om" is `updatedAt` plus the remaining minutes), lock, open parts, ignition and climate when on, outside temperature, odometer, and warnings with the API key's expiry from 14 days before.
- `cars`: every car from `car.vehicles`, one row each: levels, range, charge status, lock, open parts and warnings.
- Both show when each car was last seen (`updatedAt`) rather than marking old data: a parked car is polled up to every 12 hours. Only a stale result, from failing updates, is marked in the header. Coordinates are not shown; the data has no place name.

`car.NewFake(tb, opts...)` serves the Pleos API in-process for a Hyundai key with one car, plus a Kia key with `ExpectKey_Kia`, from the documented example responses in `modules/car/testdata` with each car's state on top. It checks brands, tokens and which key a VIN belongs to; `Expect*` options and methods set up failures. It works inside a synctest bubble.

### RSS

Reads named feeds (RSS 1.0, RSS 2.0, Atom, JSON Feed), parsed with `gofeed`. Config keys under `modules.rss`:

| Key | Default | Meaning |
| --- | --- | --- |
| `feeds` | empty | Feeds by name, each with `url` (http or https). Empty disables the module. Another URL fails `Initialize`. |

```yaml
modules:
  rss:
    feeds:
      news: { url: https://example.com/rss }
      tech: { url: https://example.org/atom.xml }
```

`Update` runs every 15 minutes; data is stale after 2 hours. Each update fetches every feed concurrently, with `If-None-Match` and `If-Modified-Since` from the feed's last response; a 304 keeps its items. A response is read up to 5 MB. Items are stored newest first (published time, else updated time; undated items last, in feed order), at most 50 per feed, with the description (else the content) as plain text cut at about 300 characters. An item without a title takes its summary as title. A feed that fails keeps its previous items with the error; the update fails only when every feed fails. A feed whose URL changed starts over: its old items and validators are dropped.

Query `rss.items` takes `feed` (a configured name; empty is the first by name) and `limit` (default 10, at most 50), and returns the feed's `name`, `title` and `link` as it publishes them, `fetchedAt` and `error` of that feed, and `items` (`title`, `link`, `published`, `summary`). An unknown feed is an error. The value is null until the feed's first fetch. A device selects its feed by overriding `feed` for the page, so one page shows a different feed per device.

`rss.NewFake(tb, opts...)` (in `feed_fake.go`) serves the RSS and Atom fixtures in `modules/rss/testdata` at `rss.FakeNewsURL` (ETag and Last-Modified) and `rss.FakeTechURL` (Last-Modified only), answering conditional requests with 304; `ExpectFeed` serves another body, `Publish` changes a feed and `Fail` makes it answer with a status.

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
	return &config{Location: "Amsterdam, The Netherlands"}
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

func (m *weather) Initialize(ctx context.Context, cfg *config, env module.Env) error {
	m.cfg = cfg
	return nil
}

func (m *weather) Enabled(ctx context.Context, q module.Query) (bool, error) {
	return m.cfg.Location != "", nil
}

func (m *weather) Update(ctx context.Context, u *module.Update) error {
	// geocode Location (cached in weather_locations through u.DB), fetch
	// the forecast with u.HTTP, then store it as the module's snapshot.
	return u.SaveSnapshot(forecast)
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
| `Initialize(ctx, *C, module.Env) error` | Receive config, set up clients. The module's migrations have run by then. No network I/O: it runs for every registered module, configured or not. |
| `Enabled(ctx, module.Query) (bool, error)` | Whether the module's schedule runs. Decided by the module, typically from its config (e.g. `weather` is enabled when `location` is set). An error, e.g. from reading its tables, marks the module failed and is retried with backoff, as from `Update`. |
| `Update(ctx, *module.Update) error` | Called by the scheduler: fetch from the source, write to storage. |

A module whose type also implements `module.AdminHandler` (`ServeAdmin(w, r, q module.Query)`) gets an admin page: it is served on the admin listener under `/<name>/` (the prefix stripped), with the module's `Query` on the writer, its shared values included, and linked from its name in the admin index's module table. Requests run concurrently with updates, so state they share goes through the database or is synchronized. A module whose `Initialize` failed is not served. A module or shared value page named after one of the admin UI's own path prefixes (`modules`, `simulate`, `images`, `preview`) stops startup. This is how settings like an API key are entered.

Options: `WithUpdateInterval` (default 15 minutes), `WithStaleAfter` (default 1 hour), `WithQuery` (see [Queries](#queries)), `WithMigrations` (see [SQL files](#sql-files)), `WithShared` (see [Shared values](#shared-values)).

Update and queries are split so a slow or failing source never blocks a screen: pages render from the last stored data.

### Configuration and lifecycle

- Every registered module is initialized with its defaults, overlaid with its `modules.<name>` section if present. Unknown keys, decode errors and a section for an unknown module stop startup: they are operator errors. So do a module registered twice and a module or shared value named after a migration owner outside `module` (`storage`, `series`, and the `Options.Reserved` `cmd/dashboard` passes: `device`), which would share its goose version table. `module` does not know the file format: `Options.Configs` holds a `module.Config` per section, decode functions that `cmd/dashboard` builds with `module/yamlconfig`.
- The scheduler asks `Enabled` before each update, with a 30 s timeout, never concurrently with an update. A disabled module skips the update and shows as disabled (not failed) in the admin UI; its queries still serve stored data, marked stale once older than `stale_after`. Because it is asked every time, a module can enable itself later, e.g. once a token appears in its table.
- Timing overrides live under a reserved `schedule` key that the service strips before decoding; `update_interval` is at least 1 minute:

```yaml
modules:
  weather:
    schedule:
      update_interval: 30m
      stale_after: 3h
```

- A source being offline never stops the service: network work happens in `Update`, whose errors mark the module failed and are retried with backoff. Each update has a 2-minute timeout. If `Initialize` returns an error, the module is marked failed and stays off until restart; its queries return that error without running. A panic in `Initialize`, `Enabled`, `Update` or a query is recovered and handled as an error from that call. A successful `Update` is recorded even if its context is canceled as it returns (shutdown, or a manual update's request going away), so module state matches the tables it wrote.
- Start: each schedule starts after a random delay of up to 30 s (or the interval, if shorter).
- Retries: exponential backoff from 30 s, doubling to at most 6 hours, not capped at the interval. A module returns `module.ErrAuth` (wrapped) for rejected credentials; that stops retries until a manual update (Update now in the admin UI) succeeds or the service restarts. The error is recorded in module state, so pages see it in the result envelope. A manual update does not ask `Enabled`; when it succeeds it resets the backoff, and the next scheduled update is an interval (or the delay set with `NextUpdate`) after it. A scheduled update that fires while a manual one runs waits for it and is skipped if it succeeded, so a rate-limited source is not called twice in a row.

### Dependencies

Dependencies are typed arguments, one type per access level, so a call can only do what its path allows and a missing dependency is a compile error. `ctx` carries only cancellation and the trace.

- `module.Env` (`Initialize`, shared value builds): `Log`, `HTTP` (30 s timeout; calls are traced as children of the update or query, except those whose context is marked with `module.WithSecretURL`), `Dialer` (for sources that are not HTTP), `Now`, `Location` (the configured `timezone`).
- `module.Query` (queries, `Enabled`, admin pages, `Versioned.Version`): `Env` plus `DB`, `LoadSnapshot(ctx, &v)`, `QuerySeries` and the module's shared values. `DB` is the read-only pool, except on admin pages, which get the writer to store settings.
- `*module.Update` (`Update`): `Query` on the writer plus the writes, `SaveSnapshot(v)`, `NextUpdate(d)` and `AddSample`. Helpers that only read take `u.Query`.

Query paths have no write methods: snapshots, the next update and series samples are written only through `*module.Update`. Module helpers take what they use (`*http.Client`, `module.ContextDialer`, `*sql.DB`) rather than a whole handle, so they are tested without a `Set`.

`u.SaveSnapshot(v)` and `q.LoadSnapshot(ctx, &v)` store and read the module's snapshot (see [State](#state)). `u.NextUpdate(d)` runs the next scheduled update `d` after this one instead of an interval later, for modules that poll adaptively; it applies when `Update` succeeds, scheduled or manual (the most recent success decides), the last call wins, and it has no effect on the backoff after a failure. A delay longer than `stale_after` shows the data as stale while the module waits.

### Shared values

A value several modules use, such as an API client, is defined once with `module.DefineShared` and configured once, under `modules.<name>` like a module. Modules that use it declare it with `module.WithShared`, optionally with declarations of type `D` (e.g. the API scopes they need), and read it with `Get(q)`, passing their `Query` (`u.Query` from `Update`):

```go
package esi

type Scopes []string

var Shared = module.DefineShared("eveonline_esi", defaultConfig,
	func(ctx context.Context, cfg *config, decls []Scopes, env module.Env) (*Client, error) { ... },
	module.SharedMigrations(migrations))
```

```go
package skills

var Module = module.Define[skills, config]("eveonline_skills", defaultConfig,
	module.WithShared(esi.Shared, esi.Scopes{"esi-skills.read_skills.v1"}))

func (*skills) Update(ctx context.Context, u *module.Update) error {
	resp, err := getskills.Request(ctx, esi.Shared.Get(u.Query), ...)
	...
}
```

- `Load` builds each shared value once, after migrations and before any `Initialize`, and only if a registered module declares it. The build function gets the decoded config, every declaring module's declarations in module name order, and an `Env`; like `Initialize`, it does no network I/O.
- `SharedMigrations` gives a shared value tables, migrated like a module's with the shared value's name as owner. Its methods reach them through the `DB` of the `Query` or `*Update` they are passed: the writer in a module's or its own `Update`, the read-only pool in queries and `Version`. A method that writes takes `*module.Update`, as `esi.Client.Token` does.
- A shared value that is a `module.AdminHandler` is served on the admin listener under `/<name>/` (the prefix stripped), with a `Query` on the writer, without shared values, and linked from the admin index under Shared. This is how logins work.
- The config section follows the module rules (defaults, unknown keys stop startup); it has a `schedule` key only when the value has a schedule (below). A section for a shared value no module declares is an unknown module.
- A build error turns off every module that declares the value, as an `Initialize` error does; the error is prefixed with the shared value's name, and their queries return it without running.
- `Get` in a module that did not declare the value panics, which surfaces as an error from that call. A shared value's name may not be a module's name.
- A shared value that implements `module.Versioned` reports the version of the source it serves (`Version(ctx, q)`, `q` on the read-only pool); the admin UI lists every shared value with its version and build error under Shared.
- Shared values have no snapshot or queries.

`module.SharedSchedule(interval, staleAfter)` gives a shared value a schedule; it must implement `module.Updater` (`Update(ctx, *module.Update) error`), checked when it is defined. It runs like a module under the shared value's name: same status in the admin UI, backoff, `ErrAuth`, Update now, `module_state` row, metrics, 2-minute update timeout and `schedule` config key. `module.SharedUpdateTimeout(d)` raises the timeout. It is always enabled. A build error lists it as failed to initialize. The SDE uses this to check for new builds.

### EVE Online

The EVE Online modules share two values, each in a subpackage of `modules/eveonline`:

- `esi.Shared` (`eveonline_esi`): the ESI client and the characters logged in through EVE SSO.
- `sde.Shared` (`eveonline_sde`): the static data, see [Static data](#static-data).

#### ESI

`esi.Shared` (`*esi.Client`, package `modules/eveonline/esi`) sends requests through `lib-esi-go`'s transport on top of the service's HTTP client transport, so ESI calls are traced like other calls. The transport includes `lib-esi-go`'s in-memory rate limiter: a request waits while its rate limit group is over 75% used. A 429 or 420 response fails its request with `*esi.RateLimitError` and stops all ESI requests of the client until the limit resets (`Retry-After` for 429, `X-Esi-Error-Limit-Reset` for 420, a minute without the header): a request in that time waits for the reset, or fails with `*esi.RateLimitError` without being sent when its deadline comes first. `*Client` is the sender for `lib-esi-go` requests. Its version in the admin UI is the ESI compatibility date requests are sent with (`lib-esi-go`'s `CompatibilityDate`). Config keys under `modules.eveonline_esi`:

| Key | Default | Meaning |
| --- | --- | --- |
| `contact` | none | Contact values in the User-Agent, e.g. `mailto:you@example.com`. Required: without it the client is not built and the EVE modules are off. |
| `client_id`, `client_secret` | empty | The SSO application from developers.eveonline.com. Empty disables character logins. |
| `callback_url` | empty | The application's callback URL: the admin listener's address with path `/eveonline_esi/callback`, as the browser reaches it. Set together with the client keys. |

Characters:

- Modules declare the scopes they need: `module.WithShared(esi.Shared, esi.Scopes{...})`. A login asks for every declared scope.
- `/eveonline_esi/` on the admin listener lists the characters with the declared scopes they have not granted, their token expiry and a rejected refresh. "Log in a character" goes to EVE SSO; the callback stores the character, its granted scopes and tokens in `eveonline_esi_characters`. Logging in a character again replaces its tokens and scopes and clears its error. Pending logins live in memory for 10 minutes.
- Remove on a character's row (`POST /eveonline_esi/remove` with `character_id`, redirecting back with 303) deletes it and its tokens from `eveonline_esi_characters`, holding its refresh lock so no refresh rotates the token meanwhile, then revokes its refresh token at SSO (`/v2/oauth/revoke`, `token_type_hint=refresh_token`, client credentials as Basic auth). A failed revoke is logged and does not undo the removal. Like the rest of the admin UI, the form has no CSRF token; the admin listener has no authentication and is restricted at the network level. Modules drop the character on their next update; queries that read per-character data from a snapshot leave out characters no longer listed, since a module with no characters left is disabled and keeps its snapshot.
- `Client.Characters(ctx, q, scopes)` returns the characters that granted all of `scopes`; a module is typically enabled when there is one. `Client.Token(ctx, u, id)` returns the character's token for `authentication.WithToken`. It refreshes itself a minute before expiry and stores the result, so it takes `*module.Update`. Refreshes are serialized per character, as a refresh may rotate the refresh token. A refresh saves only if the stored refresh token is still the one it used; otherwise a login replaced it meanwhile, and the token reloads the login's. The result is saved with its own 30 s timeout rather than the request's, since SSO has already spent the old refresh token.
- A rejected refresh (`invalid_grant`) is recorded on the character, message and error code, and fails the update with `module.ErrAuth`. From then on `Client.Token` fails with `module.ErrAuth` without asking SSO, until the character logs in again. After logging in again, Update now on each affected module resumes it.
- A rejected application (`invalid_client`: wrong `client_id` or `client_secret`) fails the update with `esi.ErrClientRejected` and `module.ErrAuth`, and is not recorded on the character.
- The access token's claims (character ID, name, scopes) are read without checking the signature: the token comes straight from the token endpoint over TLS, authenticated with the client secret.

#### Static data

`sde.Shared` (`*sde.Store`, package `modules/eveonline/sde`, no config keys) imports the datasets modules declare from the Static Data Export into typed tables, checking for a new build every hour, and answers lookups such as `Types(ctx, q, ids)`. Its version in the admin UI is the imported build. Datasets, tables, update rules and the test fake: [sde.md](sde.md).

#### Server status

`eveonline_status` (`modules/eveonline/status`) has no config keys. `Update` runs every 5 minutes (ESI caches `/status` for 30 s; at most once a minute is reasonable); data is stale after 30 minutes. While the cluster is offline ESI does not answer `/status`: a request that hits lib-esi-go's 10 s timeout (ESI's own) after connecting, or a 503 or 504 response, is stored as offline (`online: false`, other fields zero, `startTime` null) rather than failing the update. Other errors fail the update, including a timeout before connecting (DNS, dial), which is our network rather than the cluster. Each successful update while online also writes the player count to the series `eveonline_status.players` (rate).

Queries:

- `eveonline_status.server` takes no params and returns the stored status.
- `eveonline_status.history` takes [history params](#time-series) and returns the average player count per step up to now, as series points. A point is null when the server was offline or not polled throughout its step, so a chart shows downtime as a gap.

#### Skill queues

`eveonline_skills` (`modules/eveonline/skills`) has no config keys. It declares scopes `esi-skills.read_skillqueue.v1` and `esi-skills.read_skills.v1` and is enabled when a logged-in character granted both. It declares `sde.Types` for skill names. `Update` runs every 15 minutes; data is stale after 2 hours. ESI caches both for 60 s, but they only change in game: queries count down from the stored finish dates, so more frequent updates add nothing.

- `Update` fetches every such character's queue, ordered by queue position, and trained skills with total and unallocated skill points, and stores them as ESI returned them. A character whose requests fail keeps its previous data and fetch time, with the error; the update fails only when every character fails, so one rejected token does not pause the others. A character's refresh rejected, or ESI answering 401 or 403 (token revoked, scope removed in game), is an auth failure; the update fails with `module.ErrAuth` only when every character failed that way.
- Skill names come from the SDE when the query runs, so a new SDE build names skills without a skills update. A skill the imported build does not have, or any skill before the first import, has an empty name; the page shows `Skill <id>`.
- ESI keeps finished skills in the queue, and does not add them to `/skills`, until the character logs in. Queries drop entries whose finish date has passed, using the current time rather than the update's, so a queue runs out on screen between updates, and add their points (the entry's end points less the skill's stored points) to the total.
- A paused queue has entries without start and finish dates.
- Queries leave out characters that are no longer logged in with both scopes, so a removed character disappears before the next update.

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
modules/eveonline/status/sql/migrations/00002_drop_samples.sql
modules/eveonline/esi/sql/migrations/00002_auth_error_code.sql
modules/eveonline/esi/sql/queries.sql
modules/eveonline/sde/sql/migrations/00001_sde.sql
modules/eveonline/sde/sql/queries.sql
modules/p1/sql/migrations/00002_gas_days.sql
modules/p1/sql/queries.sql
sqlc.yaml                       one entry per owner
```

- **Migrations** are goose files (`-- +goose Up`), embedded with `go:embed`. Each owner has its own sequence and its own version table (`goose_storage`, `goose_device`, `goose_weather`), run through a goose `Provider` per owner, so modules number their migrations independently. `module.Load` runs the module state migrations (owner `storage`), then `series.Open` the series migrations (owner `series`), then each shared value's, then each module's, before any `Initialize`. `device.Load` checks its required options, validates the rest (positive intervals, pages per device, a known power) and runs the API key migrations (owner `device`). A failing migration stops startup.
- **Queries** are written in `queries.sql` and turned into Go by sqlc. `sqlc.yaml` at the repository root has one entry per owner whose schema is that owner's migrations; adding a module adds an entry. Generated files are committed; CI checks they are up to date (`go tool sqlc diff`); sqlc is tracked with the `tool` directive in `go.mod`.
- **Tables** are prefixed with their owner (`weather_locations`, `device_keys`); module state is `module_state`. An owner never reads or writes another owner's tables. Shared data goes through service APIs: module state through `Update.SaveSnapshot` and `Query.LoadSnapshot`.
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
- **Writer**: one connection (`MaxOpenConns(1)`), used by `Update`, admin pages, module state, device API keys (read once per device, then cached in memory: keys never change) and migrations. Code holding a writer transaction must not ask for the writer again: with one connection that deadlocks.
- **Reader**: a pool of 4 opened with `query_only(1)`, used by query functions, `Enabled`, `Version` and status reads. `Query.DB` is the reader in queries and the writer in `Update`, so a query cannot write.
- DSN pragmas apply to every new pooled connection: `busy_timeout=5000`, `foreign_keys=ON`, `cache_size=-32768` (32 MB per connection, so up to 160 MB), `mmap_size=268435456` (256 MB, shared); the writer also sets `journal_mode=WAL` and `synchronous=NORMAL`. Driver options on both: `_time_format=sqlite` and `_timezone=UTC`, so every time is converted to UTC before it is written.

### Write load

The concern is many tiny writes wearing the SSD. SQLite runs in WAL mode with `synchronous=NORMAL`, a large page cache and `mmap_size` (reads come from memory), `busy_timeout`, and one writer connection. WAL checkpoints at the default 1000 pages, so they are infrequent. Rendered PNGs, device wake data and renderer state stay in memory.

ZFS: put `/data` on its own dataset with `recordsize=16K`, matching SQLite `page_size=16384`. ZFS' default 128K records would rewrite 128K for every changed 4K page.

Backups: ZFS snapshots of the `/data` dataset. WAL mode keeps the file crash-consistent, so a snapshot restores like a power loss.

### State

The service keeps, per module, the latest status in `module_state`: `fetched_at`, `attempted_at`, `error`, plus a JSON document (`data`) for modules whose data is one snapshot (weather). A snapshot saved during `Update` is written together with `fetched_at` when `Update` returns nil, and dropped when it fails. Queries return data with its freshness (age above `stale_after` marks it stale).

Modules that need more (caches, tokens, history) define their own tables in their [SQL files](#sql-files).

### Time series

Samples taken every minute or so (power, meter readings) go to the service-owned series tables rather than module tables. A module declares each series with `module.WithSeries(name, kind)`, stored as `<module>.<name>`, writes with `u.AddSample(name, t, v)` from `Update` and reads with `q.QuerySeries(ctx, name, from, to, step)` in its queries. A module may not be named `series` or `storage`: those own the service's migrations.

| Table | Rows |
| --- | --- |
| `series_names` | id per declared name |
| `series_raw` | `(series, ts, value)`, `ts` in Unix seconds, one row per sample second |
| `series_rollup` | `(series, res, ts, value, n)` for buckets of 5 minutes, 1 hour and 1 day; `n` is the number of samples |

- **Kinds**: `series.Rate` buckets hold the average of their samples, weighted by count when combining buckets (power); `series.Counter` buckets hold the last sample (meter readings).
- **Buffer**: `Add` keeps samples in memory. Every minute the store writes them in one transaction on the writer, then recomputes the buckets of each level from the one holding the earliest new sample. A failed write keeps the samples for the next minute, up to a day's worth per series (1440 each); beyond that the series' oldest are dropped with a warning, leaving other series' samples alone. At shutdown the store flushes when the schedules stop and again after the listeners drain, so manual updates finishing in the drain are kept; a crash loses at most a minute. 5-minute buckets past the raw retention are not rebuilt. At startup the last 24 hours are rolled up again. The loop runs only when a module declares a series.
- **Alignment**: buckets under an hour align to Unix time; hours and days align to local time (`timezone`), so a day is a local midnight to midnight, 23 or 25 hours on DST dates, and the repeated hour of a fall back is a bucket of its own. Steps between an hour and a day align to the local wall clock, so a 6-hour bucket spanning a DST change is 5 or 7 hours.
- **Retention**: raw samples 7 days, 5-minute buckets 90 days, hours and days forever. Pruned hourly.
- **Queries**: `step` is at least a minute and divides a day, or is a whole number of days; at most 2000 points. The query reads the coarsest level that divides `step` and still holds `from` (raw, 5 minutes, 1 hour, 1 day), falling back to the finest that does, and groups it into steps; so 1-minute steps older than 7 days have a value every fifth minute. It returns every bucket from the one holding `from` up to `to`, as `{t, v}` with `v` null for a bucket without samples, so charts show gaps.
- **History params**: module queries for a series up to now take `series.HistoryParams`: `hours` (default 24, 1 to 2160, the 5-minute retention) and `stepMinutes` (default 15, dividing 1440). `Range(now)` validates them and returns the `from`, `to` (the second after now's; samples are whole seconds, so the bucket holding now is the last and has the latest sample) and `step` for `Query`.

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
    pages: [weather, hello]    # from the layout, else pages/shared/
    power: auto                # auto (USB-Connected, else battery), usb or battery
    params:              # optional query param overrides, <page>.<data key>
      weather.weather: {days: 5}
```

A layout is a folder of pages, `pages/layouts/<layout>/<page>.tsx`, written for one panel size; a page is named `<layout>/<page>`. Shared pages, `pages/shared/<page>.tsx`, are named `<page>` and work on any layout. Each device uses one layout and lists its pages by file name; the first page is the main page. A name resolves to the layout's page first, then the shared one, so a layout can override a shared page for its panel. A page in neither stops startup. Components and helpers live in `pages/components/` and `pages/lib/`.

Layouts `e1001` and `e1003` hold only `logo`, the setup image (see [Protocol](#protocol)), sized for their panel: the mark is centered above the bottom third, where the firmware draws its messages. Every other page is shared.

Shared pages are written in px for the E1001, 800x480 (480x800 in portrait). The renderer scales them by the factor that fits 800x480 into the panel and lays them out in the panel's size divided by it (E1003: scale 2.34, 800x600 landscape, 600x800 portrait), drawn at the panel's resolution with takumi's `devicePixelRatio`; the page's `viewport` prop is that layout size. On the E1001 the scale is 1. Layout pages get the panel's own size, unscaled, unless they export `scaled = true`: then they are laid out and scaled like shared pages. takumi resolves `em` against the parent but `rem` against a fixed 16px, not the root element, which is why sizes scale this way rather than through a root font size.

Device names and layouts may contain letters, digits, `-` and `_`; `page` is reserved as a device name (it is taken by the admin route `/preview/page/...`). MACs must be unique.

The model comes from the device's `Model` header (`reterminal_e1001`, `reterminal_e1003`); the panel size follows from the model (`render.PanelFor`). An unknown model is ignored, and a device that has not reported one yet is treated as `reterminal_e1001`. Both models get the same 4-gray treatment: the E1003 is rendered at its own resolution but with the E1001's four gray levels, so one set of styles works on both. Only the PNG carrying them differs per model (see [Pipeline](#pipeline)).

### Screen schedule

Global, under `schedule`: a wake every `interval_battery` or `interval_usb`, by the device's power, from `first_wake` up to and including `last_wake`, in local time; the last wake sleeps until the first. The default on battery is hourly from 07:00 to 00:00: 18 timer wakes a day plus button presses. A `last_wake` at or before `first_wake` is on the next day. Both intervals count from `first_wake`, so a device that changes power wakes on the other grid from its next wake.

| Key | Default | Meaning |
| --- | --- | --- |
| `first_wake` | `"07:00"` | First timer wake, `HH:MM` |
| `last_wake` | `"00:00"` | Last timer wake, `HH:MM` |
| `interval_battery` | `1h` | Interval on battery; whole minutes, from 1m to 24h |
| `interval_usb` | `5m` | Interval on USB power; whole minutes, from 1m to 24h |
| `detail_timeout` | `5m` | How long a page shown after a button press stays up |

`refresh_rate` is computed from the server clock on every request, after rendering, since the firmware starts its sleep timer once it has drawn:

- **Main page**: seconds until the next wake time. If that is 60 s or less (a device that woke just before a wake time), use the wake after it: it already shows the current data. This absorbs the download and draw time, and clock drift.
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
| `GET /api/setup` | Known MAC: HTTP 200 with `status: 200`, `api_key`, `friendly_id` (last three MAC bytes), `message`. The key is created on the MAC's first setup and stored in the database (`device_keys`); later setups return the same key. When the device's layout has a `logo` page, it is rendered for the model in the `Model` header and returned as `image_url`; the firmware keeps it as its boot screen and draws its own messages over the bottom of it. Without the page, or when the render fails, `image_url` is left out: the firmware logs a download error and keeps its previous logo, with the key saved. Unknown MAC, or a missing or invalid `ID`: HTTP 200 with JSON `status: 404`; the device shows "MAC not registered" and retries every 5 minutes. Unknown MACs are listed as pending in the admin UI (up to 32). A database error is HTTP 503. |
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

Each `/api/display` records, in memory, the wake source (`Update-Source`), battery voltage (`Battery-Voltage`), RSSI (`RSSI`), firmware version (`FW-Version`), power and the time. Pages get it as `device.wake`; the admin UI shows the last wake per device. It is lost on restart.

Power (`usb`, with `powerSource`) comes from the device's `power` setting: `usb` or `battery` (`config`), or `auto`, the default, which follows `USB-Connected` (`header`) and assumes battery without it (`default`). The TRMNL firmware only sends `USB-Connected` on boards with a readable charger (TRMNL X and Gen2); the reTerminal E1001 and E1003 send only `Battery-Voltage`, so on `auto` they count as on battery.

## Rendering

### Page files

Pages are React components in TSX, styled with CSS modules, rendered by Takumi through `takumi-js` (no browser). A page exports the data it needs and a default component that receives the results as props:

```tsx
// pages/shared/weather.tsx (abridged)
import { LineChart } from "../components/LineChart";
import { Missing, PAD_X, PageFrame, Updated } from "../components/PageFrame";
import { query, type PageProps } from "../lib/page";

export const data = {
  weather: query("weather.forecast", { hours: 24, days: 3 }),
};

type Data = typeof data;

export default function Weather({ data, viewport, timezone }: PageProps<Data>) {
  const { value: f, error } = data.weather;
  if (!f) return <PageFrame><Missing what="weerbericht" error={error} /></PageFrame>;
  return (
    <PageFrame title={f.location.name} status={<Updated result={data.weather} timezone={timezone} />} gap={12}>
      <LineChart values={f.hourly.map((h) => h.temperature)} width={viewport.width - 2 * PAD_X} height={120} />
    </PageFrame>
  );
}
```

- `data` is static: `query()` only builds a descriptor (`{module, query, params}`). Components never fetch; everything arrives as props.
- Each `data.<key>` is a result envelope: `{value, fetchedAt, stale, error}`. Pages decide how to show stale or failed data.
- Props are `data`, `device` (`name`, `model`, `rotation`, `wake`), `viewport`, `system` (`staleModules`: modules whose data is stale), `now` and `timezone`.
- `PageProps<Data>` types each key from the generated query types.
- CSS modules: `import styles from "./x.module.css"`; Bun's bundler scopes class names and emits one CSS file per page. Takumi supports a CSS subset: class selectors, flexbox and grid, borders, fonts. Complex selectors and some properties may not render.
- Inline `<svg>` works: charts are components that emit SVG.

### Home page

`home` (`pages/shared/home.tsx`) is the main page on both devices, overridden on the E1001 by its layout's own (below): one `SummaryRow` per source with its key value, so a failing source is one row, not the page. Rows, in order:

| Row | Value | Detail | Queries |
| --- | --- | --- | --- |
| Weer | temperature now, conditions | today's max / min, rain in mm and its chance | `weather.forecast` |
| Energie | net flow, solar power now | today's kWh taken, returned and from solar | `p1.actual`, `solar.production`, `solar.power` |
| the car, or Auto's for several | one car: its levels (`levels`) and longest range; several: each car's levels | how each plugged-in car charges (`chargeLabel`) | `car.vehicles` |
| EVE server | online, players | VIP | `eveonline_status.server` |
| Skillqueue | the character whose queue needs attention first, time left | end time, characters that failed to update | `eveonline_skills.queue` |
| Nieuws | latest headline of the first feed | published time, feed title | `rss.items` |
| Agenda | the next event today or tomorrow that has yet to start, else one under way | day and time (`dayLabel`, `eventTime`), how many more, calendars that failed | `calendar.upcoming` |

- No query returns today's solar energy: the row sums the 15-minute `solar.power` averages since local midnight (`kwhToday`), the current bucket up to `now`. "Taken" is grid import from the meter, not household use.
- A stale row shows `verouderd` and its fetch time; a row without data shows `Geen ...` and the error. A row built from several queries (Energie) shows what it has and is stale when any of them is.
- The skill queue row picks among characters with data (`sortQueues`); characters with an update error are counted in its detail.
- A device picks the feed with `home.news: {feed: ...}`.

### Layout home pages

`e1001/home` and `e1003/home` override `home` on their panels. Both are `HomeScreen` (`pages/components/HomeScreen.tsx`), laid out 800 px wide; `e1003/home` exports `scaled = true` and leaves out the skill queues (its data is `homeData`; `e1001/home` adds `homeSkills`). They show values as pictures, text only for the values themselves and for headlines. values as pictures, text only for the values themselves and for headlines. In landscape:

| Block | Shows | Queries |
| --- | --- | --- |
| right, `HouseScene` | solar power now and today's kWh on the roof; today's kWh taken and returned inside the house, the net flow now under them; the car beside it with its first level as a battery, a bolt and cable while charging, the charge target and end time, else its longest range and when it was seen | `solar.production`, `solar.power`, `p1.actual`, `car.vehicle` |
| left, top | characters whose skill queue ends within a week, is empty or paused (`skillWarnings`, at most 3): a bar of the time left on a 7-day scale, the training skill, the time left; left out when there are none | `eveonline_skills.queue` |
| left | headlines of one feed with their age, as many as fit under the skill queues | `rss.items` |
| bottom | five day cards: weather icon (`weatherIcon`), max / min, chance and amount of rain; the day's events (`dayEvents`), all-day first in gray, at most 6 rows with `+N meer` | `weather.forecast`, `calendar.upcoming` |

- Top right, small (`DeviceStatus`), from the device's last wake: a Wi-Fi icon with its quality (`wifiQuality`, 2 x (RSSI + 100) as a percentage), then a battery icon with its charge (`batteryPercent`), or a bolt and `USB` on USB power; each icon has three arcs or segments, filled from 1, 34 and 67%. Under them, right-aligned, `Updated:` and the render time. The charge comes from a generic single-cell lithium resting-voltage curve, not measured on the reTerminals.
- A block whose result is stale or failed shows a black "!" (`StaleMark`) instead of a fetch time; so do the agenda when a calendar failed, the skill queues when a character failed and the headlines when the feed failed.
- After sunset today's card shows the temperature now instead of the day's range. Under 10 W of solar is idle: the roof box turns gray, with a moon once the sun is down.
- Without a car (`car.vehicle` empty without error) the car is left out. A device picks the car with `home.car: {id: ...}` and the feed with `home.news: {feed: ...}`.
- In portrait the house goes above the headlines and the bottom shows three days.
- The day cards take the height left under the top block, so their event rows follow the panel: 6 at 800x480, 13 at 800x600 (the E1003, scaled).
- Without skill queues (`e1003/home`) the headlines take the whole left column.

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

func (m *weather) forecast(ctx context.Context, q module.Query, p forecastParams) (*Forecast, error) {
	var f Forecast
	ok, err := q.LoadSnapshot(ctx, &f)
	// trim to p.Hours and p.Days from now
}
```

- Params are the page's declared params, with the device's overrides merged over them, decoded as JSON into the query's own struct. Unknown keys and bad values fail at startup (for every page and every device override), not at render.
- Results are plain structs that serialize to JSON; TypeScript types for them are generated (see [Types](#types)).
- Queries read storage only, never the source: their own tables through sqlc and module state, on the read-only connection.

### Renderer process

A long-running Bun process (`bun renderer/main.ts --pages pages [--watch]`, run in `renderer.dir`), started on the first request and supervised by the Go service. Per-render process start would cost 50-100 ms; a resident process renders in ~20 ms (measured, 800x480).

- **Bundling**: at start the renderer bundles every `pages/layouts/<layout>/<page>.tsx` and `pages/shared/<page>.tsx` with `Bun.build` into JS plus CSS. Bun writes one CSS file for entries that import the same CSS modules, so a page left without one is built again on its own for its CSS. With `renderer.watch` on, for development, it rebuilds on file changes; a failed rebuild keeps the previous bundle, and a watcher error is logged and ends rebuilding.
- **Protocol**: length-prefixed frames over stdin/stdout, one request at a time. Each frame is a 4-byte big-endian length, then a JSON header, then for render responses the raw PNG bytes (header carries the PNG length). No base64, no line-length limits. Covered by tests on both sides.

| Request | Response |
| --- | --- |
| `{"id":1,"op":"describe"}` | `{"id":1,"pages":[{"name":"weather","data":{...}}]}` |
| `{"id":2,"op":"render","page":"weather","props":{...}}` | header `{"id":2,"png":<bytes>}` + PNG, or `{"id":2,"error":"..."}` |

- **Logging**: stdout is reserved for frames; the renderer redirects `console.log` to stderr at start, and Go logs stderr lines. A failed render logs the page and stack there; the error frame carries only the message.
- **Requests**: a type guard checks each request header (`id`, `op`, and for render `page` and `props` with `viewport` and `data`); an invalid one gets an error frame.
- **Supervision**: a process that exits is replaced on the next request, with backoff from 500 ms to 30 s between starts. Each request has its own timeout, 30 s for a render and 60 s for describe (it may bundle); a process that does not answer in time is killed and restarted, so requests queued behind it are not stuck. A request in flight when the process dies fails; the device keeps its last image and gets it again on the next wake.
- **Callers leaving**: when a caller's context ends (a device disconnects), the caller gets its error at once, while the response is still read in the background, within the same timeout, before the next request uses the process. Only a response that does not arrive in time restarts the process.

### Pipeline

1. **Describe**: the renderer reports pages and their `data`. Go asks on every use, so a watch rebuild is seen by the next request. At startup Go checks that every page's `module.query` exists and its params decode into the query's params type, with each device's overrides too; errors stop startup. Pages from a later rebuild are not checked again: a bad query shows as an error envelope.
2. **Resolve**: Go runs the page's queries concurrently with a 10 s timeout. One failing query yields an error envelope, not a failed page.
3. **Render**: Go sends props to the renderer, gets an RGBA PNG at `props.viewport`.
4. **Reduce** (Go): rotate if needed, map each pixel to one of 4 gray levels (0, 85, 170, 255) without dithering, which keeps text sharp, and encode a 2-bit PNG. A pixel takes the darker of its two neighboring levels unless it is within a quarter step of the lighter one: anti-aliased stems of small text are thinner than a pixel on the E1001, and nearest rounding renders them a level too light. Exact levels are unchanged. Both models use 4 levels; the PNG differs per panel (`render.Panel`). The E1001 gets a 2-bit PNG. The E1003 gets a 4-bit PNG with the levels at 0, 3, 7 and 15 of 16: the firmware expands a 2-bit PNG evenly to 0/5/10/15 on the E1003's IT8951, which shows both grays too light, and passes a 4-bit PNG through as is. TRMNL firmware before usetrmnl/firmware#671 garbles 4-bit gray PNGs.
5. **Check**: the PNG must be under 750 KB (both models; firmware limit with PSRAM). An image without dithering is far below that (every page at 1872x1404 in 4 bits: at most 56 KB); exceeding it fails the render, which is logged, and the device gets its previous image.
6. **Name**: `filename` is the PNG's hash plus timestamp (see [Protocol](#protocol)). An image identical to the device's previous one keeps its filename and the device skips the download. Props include `now` and wake values, so most wakes produce a new image; that is accepted.

### Types

Go query types are the source of truth. `cmd/gentypes` walks every registered query's params and result types by reflection and writes `pages/generated/queries.ts`: one interface per struct (named module + Go type name, e.g. `WeatherForecast`; a struct from a package other than the module's is named for that package, e.g. `SeriesPoint`, params included, e.g. `SeriesHistoryParams`), and a `Queries` map from `"module.query"` to `{params, result}`. It also writes `pages/generated/props.ts` from `render.Props` and the types it uses (`Device`, `Wake`, `Viewport`, `System`, `Result<T>`, `Props<D>`); `screen.PowerSource` becomes a union of `screen.PowerSources`. `page.ts` re-exports them. `query()` and `PageProps` use both, so a typo in a query name, a wrong param or a wrong field is a type error in the editor and in CI (`tsc --noEmit`). The generated files are committed; CI checks they are up to date.

Mapping rules: `time.Time` is a string (RFC 3339), pointers are `T | null`, and exported fields are named by their `json` tag (`omitempty` and `omitzero` make them optional). Slices are typed without `null`, so queries return non-nil ones. Params fields are all optional: a missing param is the Go zero value, which a query treats as its default. Embedded fields, maps, `[]byte`, interfaces, the `,string` option and types with their own JSON encoding are errors; gentypes supports a shape once a query needs it.

Pages declare `type Data = typeof data` and use `PageProps<Data>`: destructuring `data` from props would otherwise shadow the module's `data` in its own type.

### Shared code

- `pages/lib/`: `page.ts` (`query`, `PageProps`, prop types), `format.ts` (Dutch dates, times, short day names, ages, numbers, temperatures and millimeters with a decimal comma; date formats are built once per timezone), `chart.ts` (scales, SVG points, padded ranges, series runs broken at gaps), `energy.ts` (energy units, flow direction, late day starts, the energy chart's domain and local hour ticks, today's kWh from a power series), `weather.ts` (weather code labels and icons, wind direction), `eveonline.ts` (skill levels, skill queues by urgency), `car.ts` (car names, levels, range, charge target and end, open part and warning labels, key expiry, last seen), `calendar.ts` (agenda days, rows that fit, day labels, event times, events per day, short dates, weekends), `device.ts` (Wi-Fi quality from RSSI, battery charge from its voltage), `home.ts` (combined row status, the cars row, the most urgent skill queue, the next event; for the E1001 home: skill queues running out and their bar, what the house scene shows of a car). Pure functions; layouts keep derivation here so it is tested.
- `pages/components/`: `PageFrame` (the screen padding and a header with a title and `Updated`, which shows when data was fetched and marks it stale; `Missing` for "Geen ..." states; one shared CSS module), `LineChart` (a line over optional bars, as inline SVG), `EnergyChart` (solar as an area and net flow as a line over a `ChartDomain`), `SkillQueues` (each character's skill queue, most urgent first, in the width and height it is given: skill points and as many upcoming skills per character as fit, none when only the queue ends fit, more columns when the rows do not; narrow columns show how long a queue lasts without its end time), `CarSeen` (when a car was last seen, marked only when its result is stale), `HouseScene` (house and car with solar, energy and charge values, as SVG with the values over it), `WeatherIcon` (weather icons as SVG, inverted for a black background), `StaleMark` (the "!" on stale or missing data, placed or inline), `HomeScreen` (the layout home pages: house scene, skill queues when given, headlines and day cards, sized to the viewport), `DeviceStatus` (update time, Wi-Fi and battery in small text), `SummaryRow` (a label, a value and a detail line for one source, `verouderd` with the fetch time when stale, `Geen ...` with the error when missing; the label beside the value, or above it with `stacked` for portrait; `prose` for text values such as headlines and lists, smaller and clamped to one line, two when stacked).

Time: the config's `timezone` is the single source. Go loads it with `time/tzdata` embedded, computes everything in it, and passes `now` and all timestamps to pages as ISO 8601 strings with offset, plus `timezone` in props. Formatting uses `Intl` with `nl-NL`; a `bun test` asserts the runtime's ICU data formats Dutch dates as expected.

### Testing

Each step has its own test with no network:

| What | Tool | How |
| --- | --- | --- |
| Module `Update` and queries | `go test` | `moduletest.Load` loads modules on a temp database; `moduletest.HTTPClient(handler)` serves every request in-process (works inside a synctest bubble). Fakes live in `<name>_fake.go` with `NewFake(tb, opts...)` and `Expect<Thing>` options: `weather.NewFake` (Open-Meteo, fixtures in `modules/weather/testdata`), `p1.NewFake`, `calendar.NewFake`, `car.NewFake`, `rss.NewFake`, `solar.NewFake`, `esi.NewFake` (SSO), `status.NewFake`, `sde.NewFake`. Assertions on the query results |
| Module loading, scheduler | `go test` | fake modules: config errors, panics, backoff, auth pause, manual updates, snapshots |
| Storage | `go test` | pragmas and page size, reader cannot write, per-owner migrations |
| Device protocol | `go test` | setup, keys surviving a restart, auth, page selection, refresh rate (including DST), render failure, image store |
| Pipeline, renderer process | `go test` | fake renderer and queries, including failing and stale ones; frame encoding; timeouts, restarts, backoff; one integration test against the real Bun renderer |
| Service and admin UI | `go test` | config parsing, startup checks, previews, simulator, `/healthz`, `/metrics`, request spans, shutdown drain |
| Types | `go test`, `tsc --noEmit` | gentypes against a golden file; pages against generated types |
| Formatting, renderer frames, pages | `bun test` | table tests on pure functions; frame encoding, the request guard and rendering in `renderer/`; every page rendered at both orientations with typed fixtures (missing, stale and populated data); every CSS module class a component uses exists |

### Fonts

Takumi's built-in sans-serif, a variable font covering all weights. No font files are loaded.

## Admin UI

On a separate admin listener. No authentication; access is restricted at the network level (bind addresses in config, TrueNAS port mapping, firewall rules).

| Route | Purpose |
| --- | --- |
| `GET /` | Module status (phase, interval, last fetch and attempt, next run, error, Update now), shared values with their version, devices with their last wake and page previews, pending devices, every page on one panel. `?panel=<model>-<rotation>` (e.g. `reterminal_e1001-90`) picks it from a select of each model at 0 and 90, so it survives a reload; it defaults to the first device's model and rotation, else `reterminal_e1001` at 0. Without JavaScript a button submits the select |
| `POST /modules/{module}/update` | Run one update of the module now, then redirect to `/` |
| `GET /preview/{device}/{name}.png` | Exactly what the device gets; `{name}` is a page name, `<layout>/<page>` or a shared `<page>`; `?model=` overrides the device's model |
| `GET /preview/{device}/{name}/props.json` | The props the page receives; `?model=` as above |
| `GET /preview/page/{name}.png?model=...&rotation=...` | A page on a panel without a device; model defaults to `reterminal_e1001`, rotation to 0 |
| `GET /simulate/{device}` | Simulator page for a device |
| `POST /simulate/{device}/wake?source=timer\|button&model=...` | Plays a device wake (setup if no key yet, then display) through the device protocol in-process; returns the requests and responses as JSON |
| `GET /images/{filename}.png` | Images rendered for simulated wakes |
| `/{name}/...` | Pages of modules and shared values that are `module.AdminHandler`s, e.g. `/eveonline_esi/` (characters, SSO login and callback); linked from `/` in the module and Shared tables |
| `GET /healthz` | Liveness, `ok`; `dashboard healthz [addr]` (default `127.0.0.1:8081`) checks it for the image's `HEALTHCHECK` |
| `GET /metrics` | Prometheus metrics, see [Observability](#observability) |

The simulator runs on its own `device.Devices`, mounted only on the admin listener: its page, wake and image store (4 images) are separate from the real devices', so it never evicts their images. It shares the stored API keys, which belong to a MAC rather than to a device's state. The device listener has no simulator mode.

## Observability

### Metrics

Prometheus metrics on the admin listener at `/metrics`, next to the Go runtime and process metrics. Each package that records metrics takes a `prometheus.Registerer` in its options (`module.Options.Metrics`, `device.Options.Metrics`, `render.PipelineOptions.Metrics`) and keeps its collectors in a struct, not in package variables; nil records to no registry. `main` passes the default registry; tests pass their own, so their assertions are exact. Registering the same metrics twice on one registry fails the load.

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

The simulator's `device.Devices` gets no registry, so simulated wakes are not counted in the device metrics.

### Traces

OpenTelemetry traces, exported over OTLP/HTTP when `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` is set; without either, spans are dropped. The other standard `OTEL_*` variables apply, e.g. `OTEL_SERVICE_NAME` (default `dashboard`). Incoming `traceparent` headers are honored. `module` and `render` take a `trace.TracerProvider` in their options (`Tracing`; nil is a no-op); `main` passes the global provider it installs, which the request spans also use, so spans nest.

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
  interval_battery: 1h
  interval_usb: 5m
  detail_timeout: 5m

renderer:                      # defaults shown
  bun: bun                     # Bun executable
  dir: .                       # holds renderer/, pages/ and node_modules
  watch: false                 # rebuild pages on change, for development

modules:
  weather:
    location: "Amsterdam, The Netherlands"
    model: knmi_seamless

devices:
  hallway:
    mac: "AA:BB:CC:DD:EE:FF"
    rotation: 0
    layout: e1001
    pages: [weather, hello]
    power: auto                # auto (USB-Connected, else battery), usb or battery
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
