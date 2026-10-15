# dashboard

E-ink dashboards for two Seeed reTerminal displays at home, running TRMNL firmware against a self-hosted server:

- E1001: 7.5", 800x480, 4-level gray
- E1003: 10.3", 1872x1404, 16-level gray

Content: car status, weather, calendar, power usage, solar production.

- [docs/design.md](docs/design.md): service design
- [docs/research.md](docs/research.md): firmware options, data sources, requirements
- `tools/dtu_read.py`: read-only Modbus check against the Hoymiles DTU

## Development

Needs Go and Bun.

```sh
bun install
cp config.example.yaml config.yaml
go run ./cmd/dashboard -config config.yaml
```

Open http://127.0.0.1:8081/ for previews of every page per device and panel, and a simulator per device that wakes it through the real device API (timer or button). With `renderer.watch`, page edits rebuild on save; reload the browser.

Tests: `go test -race ./...`, `bun test`, `bunx tsc --noEmit`.

Device API keys are kept in memory until storage lands. After a restart, the first token a configured MAC presents is accepted, so anyone on the device network who knows a MAC can claim it first. Keep the device listener on a trusted network.
