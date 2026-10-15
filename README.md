# dashboard

E-ink dashboards for two Seeed reTerminal displays at home, running TRMNL firmware against a self-hosted server:

- E1001: 7.5", 800x480, 4-level gray
- E1003: 10.3", 1872x1404, 16-level gray

Content: car status, weather, calendar, power usage, solar production.

- [docs/design.md](docs/design.md): service design
- [docs/research.md](docs/research.md): firmware options, data sources, requirements
- `tools/dtu_read.py`: read-only Modbus check against the Hoymiles DTU
