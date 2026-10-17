# Plan: dashboard service

Specification of the parts of the service that are designed but not built. The built system is described in [design.md](design.md); this document uses its terms and only lists what it adds.

## Modules

| Module | Source | Data |
| --- | --- | --- |
| `p1` | Smartstuff P1 dongle, local HTTP | state + series (import/export power, energy, gas) |
| `solar` | Hoymiles: S-Miles Cloud, or DTU Modbus once the DTU answers | state + series (production per inverter, alarms) |
| `calendar` | Google Calendar secret iCal URL | snapshot (upcoming events) |

Further candidates: Plex recently added, waste collection, public transport.

Configuration:

```yaml
modules:
  p1:
    url: http://192.168.5.180
    schedule:
      update_interval: 1m
  calendar:
    ical_url: ${CALENDAR_ICAL_URL}
```

Authentication errors from these sources (HTTP 401/403) are returned as `module.ErrAuth`.

### Contract additions

Added to the module contract when a module needs them: active hours, backfill hooks, a shared key-value store.

## Time series

For P1 and solar history graphs and device battery. Schema `series(name, ts, value)` with `ts` in integer Unix seconds to keep rows small, with rollup tables at 5 minutes, 1 hour and 1 day. Retention: raw 7 days, 5-minute 90 days, hourly and daily forever. Queries pick the coarsest resolution that still fills the graph.

Modules write and read series through `module.Series(ctx)`: writes are buffered, reads are bucketed and pick the rollup. Series tables are owned by the service; modules do not access them directly.

Each series declares a kind, which decides rollups and usage:

| Kind | Example | Rollup | Usage over a period |
| --- | --- | --- | --- |
| `rate` | P1 power (W), solar power (W) | time-weighted average; gaps are not filled | average x covered time |
| `counter` | P1 meter readings (kWh, m³), inverter total Wh | last value | difference; a drop is a reset (meter or inverter replaced): count from the new value |
| `daily_counter` | S-Miles and DTU "today Wh" | last value | value at end of day; resets at local midnight |

- Gaps (P1 reboots, Wi-Fi drops) stay gaps: a bucket without samples has no value and charts show a break. Counters bridge gaps naturally.
- Buckets of an hour or longer align to local time (Europe/Amsterdam); a day has 23 or 25 hours on DST dates.
- Backfill: the P1 dongle's own hour/day/month history can seed the counters on first start.

### Write buffering

Anything sampled every minute goes through the series buffer: modules write samples into memory, and the service flushes them in one transaction per minute on the writer connection. Losing up to a minute on a crash is accepted. The buffer is flushed on shutdown. Device health series are written with the same flush. Queries read SQLite only, so buffered samples show up within a minute.

Estimate: one flush per minute touching a handful of pages is roughly 100-200 KB per minute including WAL and checkpoint, about 50-100 GB per year. Consumer SSDs are rated for hundreds of TB written. The estimate is to be confirmed by measuring bytes written per day.

### Storage jobs

The scheduler runs the series flush (every minute), rollups and retention, on the writer connection.

### Queries on series

```go
type powerParams struct {
	Range time.Duration `json:"range"`
	Step  time.Duration `json:"step"`
}

var Module = module.Define[p1, config](
	"p1",
	defaultConfig,
	module.WithQuery("power", (*p1).power),
)

func (m *p1) power(ctx context.Context, p powerParams) (Series, error) {
	// series are service-owned; the service picks the rollup and buckets
	return module.Series(ctx).Query(ctx, "p1.power_import", p.Range, p.Step)
}
```

### Data volume

24 hours of one-minute samples is 1,440 points per series; P1 import, P1 export and solar together are ~4,300. The E1001 is 800 pixels wide, so more points than pixels add nothing. Series queries take a `step` and bucket in SQL (average for power, last value for meter readings): 5 minutes gives 288 points per series. Rollup tables serve longer ranges. Chart components bucket again when given more points than their pixel width.

## Device health

Recorded per `/api/display`: battery voltage (also as a series), charging (`Battery-Charging`), USB connected (`USB-Connected`), RSSI, firmware version, wake reason, last seen. Battery voltage converts to a percentage through a resting-voltage table per model (the firmware measures before Wi-Fi and the display draw current).

Health reaches people through the screens: the `system` prop carries this device's battery and stale modules, plus the other devices' last seen and battery. A unit that runs flat shows up on the other one.

Device health is a service concern, not a module.

## Device logs

`POST /api/log` stores device log entries in the database, kept 30 days.

## Pages

Example of a page using the planned modules and components:

```tsx
// pages/layouts/e1001/overview.tsx
import { query, type PageProps } from "../../lib/page";
import { Screen } from "../../components/Screen";
import { AreaChart } from "../../components/AreaChart";
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

### Shared code

- `pages/components/`: `Screen` (frame with device status from `system`), cards, charts (`AreaChart`, `BarChart`, `Sparkline` as inline SVG, next to `LineChart`), event list. Shared components (`Screen`, cards) give a default presentation for stale or failed data.
- `pages/lib/`: formatting of durations and "updated at" labels.

### Page tests

`bun test` renders fixture props (`pages/testdata/<page>.json`), reduces them to 4 grays and compares them to a golden PNG with a small pixel tolerance; a mismatch writes a diff image. Goldens are regenerated deliberately on Takumi or font upgrades.

Fixtures come from the live service: `/preview/{device}/{layout}/{page}/props.json` serves the resolved props, saved as-is into `pages/testdata/`.

## Admin UI

| Route | Purpose |
| --- | --- |
| `GET /queries` | Every registered query with its params and a sample result |

## Deployment

- One Docker image, multi-stage: Go service, Bun runtime with the renderer and `node_modules`, pages. Base is a slim image with Bun (~60-90 MB); the Go binary is static. Pages change through a new image; the config mount holds no pages.
- Built by GitHub Actions, pushed to GHCR.
- Runs on TrueNAS with `/config` (YAML) and `/data` (SQLite, own ZFS dataset, `recordsize=16K`) mounts.
- Restart policy `unless-stopped` and a Docker health check on `/healthz` of the admin listener.
- Backup: ZFS snapshots of the `/data` dataset.

## Open questions

1. To verify on hardware: `Update-Source` values for a button wake, which button wakes each model, firmware rotation direction, resting-voltage table for the battery percentage.
2. Takumi CSS coverage: limits are found as pages are built.
3. Write load: bytes written per day, compared with the estimate in [Write buffering](#write-buffering).
