# dashboard

E-ink dashboards for two Seeed reTerminal displays at home, running TRMNL firmware against a self-hosted server:

- E1001: 7.5", 800x480, 4-level gray
- E1003: 10.3", 1872x1404, 16-level gray (rendered with 4 levels)

Pages: weather (Open-Meteo), `energy` (smart meter via `p1` and solar panels via `solar`), `energy_day` (net flow and solar power over the last 24 hours), `eveonline` (EVE Online server status), `eveonline_skills` (skill queues of the logged-in characters) and `hello`, a page showing what the service knows about the device. The `car` module has no page yet. Calendar is planned.

- [docs/design.md](docs/design.md): service design, as built
- [docs/sde.md](docs/sde.md): EVE Online static data: datasets, tables and updates
- [docs/plan.md](docs/plan.md): planned modules and features
- [docs/research.md](docs/research.md): firmware options, data sources, requirements
- `tools/dtu_read.py <host>`: read-only Modbus check against the Hoymiles DTU

## Development

Needs Go (version in `go.mod`) and Bun. sqlc is a Go tool dependency: `go tool sqlc`.

```sh
bun install
cp config.example.yaml config.yaml
go run ./cmd/dashboard -config config.yaml
```

Without `-config` the service reads `/config/dashboard.yaml`.

Open http://127.0.0.1:8081/ for module status, previews of every page per device and panel, and a simulator per device that wakes it through the real device API (timer or button). With `renderer.watch`, page edits rebuild on save; reload the browser.

Prometheus metrics are at http://127.0.0.1:8081/metrics. Traces are exported over OTLP/HTTP when `OTEL_EXPORTER_OTLP_ENDPOINT` is set, e.g. `http://localhost:4318`. See [Observability](docs/design.md#observability).

After changing SQL or query types: `go tool sqlc generate` and `go run ./cmd/gentypes`; both outputs are committed.

CI runs:

```sh
go vet ./...
go run ./cmd/gentypes && git diff --exit-code pages/generated
go test -race ./...        # needs bun install for the renderer integration test
bun test
bunx tsc --noEmit
go tool sqlc diff
```

## Docker

```sh
docker build -t dashboard .
docker run --rm -p 8080:8080 -p 8081:8081 \
  -v "$PWD/config.yaml:/config/dashboard.yaml:ro" -v "$PWD/data:/data" dashboard
```

In the container config, set `listen.admin` to `0.0.0.0:8081` so the admin UI is reachable through the port mapping, and drop `data_dir` (defaults to `/data`). The health check expects the admin listener on port 8081. The service runs as uid 1000 (`bun`), which needs write access to `/data`.

Publishing a GitHub release pushes the image to `ghcr.io/xaroth/dashboard`, tagged with the release version.

## Devices

Point the device at the server in the TRMNL Wi-Fi captive portal (Advanced > Custom Server, the `listen.device` address) and add its MAC under `devices` in the config. An unconfigured MAC shows as pending in the admin UI.

- `/api/setup` creates the device's API key on first setup and stores it in SQLite (`device_keys` in `data_dir`), so keys survive restarts. A token that does not match the stored key is rejected with 401.
- A device whose key is not in the database (set up against another database, or the database was lost) keeps getting 401: hold its button 15 s to run setup again.
- `/api/setup` hands the key to any client presenting a configured MAC (the TRMNL protocol has no other identity). Keep the device listener on a trusted network.
- The admin listener has no authentication; bind it to a trusted interface.
