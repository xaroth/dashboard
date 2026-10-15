# Research: reTerminal E1001 / E1003 dashboards

Status as of 2026-10-15.

## Hardware

| | E1001 | E1003 |
| --- | --- | --- |
| Panel | 7.5" mono, 4-level gray | 10.3" mono, 16-level gray |
| Resolution | 800x480 (landscape) | 1404x1872 (portrait) |
| Refresh | 2-5 s, partial refresh | 2-3 s, partial refresh |
| Controller | SPI, `waveshare_epaper` `7.50inv2` | IT8951 T-CON, `it8951` `seeed-reterminal-e1003` |
| Touch | none | capacitive (GT911), only usable in Seeedash |
| Battery | 2000 mAh, ~3 months at 6 h interval | 3000 mAh, ~6 months at 1 refresh/day |

Shared: ESP32-S3, 8 MB octal PSRAM, 32 MB flash, 2.4 GHz Wi-Fi only, BT 5.0, 3 buttons, buzzer, SHT4x temp/humidity, PCF8563 RTC, microSD, battery voltage sense, USB-C.

Battery figures assume rare refreshes. A 5-15 min refresh interval will cut life to weeks; plan for USB power or periodic charging.

## Firmware options

### 1. SenseCraft Seeedash (stock)

- Cloud no-code designer, free while in beta, account required.
- Built-in data: weather, Google Calendar, RSS, stocks, SenseCAP sensors.
- No local URL support (per hands-on reviews), so no direct access to Home Assistant or LAN services.
- Verdict: does not cover car status or power usage. Not suitable.

### 2. TRMNL firmware

Supported: E1001 FW >= 1.6.7, E1003 FW >= 1.8.7. Flash via usetrmnl.com/flash (USB-C, browser). Keep "Firmware Early Release" and "OTA Updates" off on Seeed hardware; official-TRMNL builds can break it. E1003 touch unused, buttons only.

Two modes:

- **Hosted (trmnl.com)**: needs a BYOD license, $50 one-time per device ($100 for both). Plugin marketplace (weather, calendar, etc.), private plugins via API/webhook. Home Assistant via the "TRMNL HA" add-on, which screenshots HA dashboards and pushes them to a "Home Assistant Screenshot" plugin webhook. Data leaves the LAN.
- **BYOS (self-hosted)**: no license. Set Advanced > Custom Server in the Wi-Fi captive portal. Device polls `GET /api/display` with `Access-Token`, gets `image_url`, `filename` (cache key), `refresh_rate` (s). Servers: Terminus (Ruby, reference), LaraPaper (PHP, Docker, has HA example plugin), plus Python/JS/Elixir implementations.

### 3. ESPHome (Home Assistant)

- Officially supported for both; E1003 needs ESPHome >= 2026.7.0.
- Device becomes an HA device: battery, temp/humidity, buttons exposed as entities. Buttons can trigger HA actions (next page, etc.).
- Content options:
  - Native ESPHome lambda drawing with HA sensors. Full control, verbose, per-device layout code.
  - `online_image` pulling a rendered PNG/BMP of an HA dashboard from the Puppet add-on (`http://<ha>:10000/<dashboard>?viewport=800x480&eink=2`). Dashboards built in Lovelace. Community notes: 1-bit BMP (`bmp_mode=binary`) shrinks 800x480 to ~48 KB; buffer size may need raising.
- Fully local, free.

### 4. Arduino / PlatformIO / ESP-IDF

Possible, but rebuilds what TRMNL and ESPHome already provide. Not worth it.

## Constraints

- Own server available (Docker).
- No Home Assistant: too much setup and upkeep for this use.

This rules out ESPHome+Puppet and the TRMNL HA add-on. ESPHome can run without HA (`online_image` from any URL), but then it only duplicates what TRMNL firmware already does, with firmware we maintain ourselves.

## Approach

TRMNL firmware on both units, pointed at a self-hosted BYOS server. No license, all local.

The device side is solved: it wakes, calls `/api/display`, downloads an image, sleeps. All work is on the server: fetch data, render a screen per device, serve it.

Server options:

1. **LaraPaper** (PHP, Docker, SQLite). Web UI, TRMNL recipe catalog, polling plugins (JSON URL + template), HTML rendered via Browsershot/puppeteer. Lists E1001; E1003 not listed, needs checking.
2. **Terminus** (Ruby, TRMNL reference). Same idea, less UI.
3. **Own service** implementing `/api/setup`, `/api/display`, `/api/log`. Small protocol, full control over rendering and both resolutions, but we write and maintain the renderer.

Preferred: try LaraPaper first; fall back to an own service if E1003 support or custom data sources fight it.

TRMNL lists the E1003 as 1872x1404, 16 grays; Seeed lists 1404x1872. Orientation to confirm on device.

## Data sources without Home Assistant

| Need | Source | Notes |
| --- | --- | --- |
| Weather | Open-Meteo or KNMI API | Free, no key (Open-Meteo) |
| Calendar | ICS / CalDAV URL | Google, iCloud, Nextcloud all expose ICS |
| Power usage | P1 meter local API (e.g. HomeWizard), inverter API | Depends on hardware |
| Car status | Hyundai Bluelink EU cloud API | Kona 2024; needs adapter, see below |

Weather and calendar fit LaraPaper polling plugins directly. Car and power may need a small adapter on the server that exposes clean JSON.

## Car: Hyundai Kona 2024 (Bluelink EU)

No official public API. Use `hyundai_kia_connect_api` (Python, the library behind the HA `kia_uvo` integration) in a small adapter container that exposes JSON for the dashboard server.

- **Auth**: EU username/password login supported directly since v4.12.0. Fallback: generate a refresh token via a browser tool (KiaHyundaiToken in the library repo, or bluelink-refresh-token); valid ~180 days.
- **Data**: SoC, range, charging state, locks/doors, climate, odometer, location, last update time, trip info (EU).
- **Cached vs forced**: cached reads return the last state the car pushed to the cloud (car pushes on events and at every 10% SoC). Forced refresh wakes the car via the modem.
- **Limits**: EU API is daily rate-limited (cap undocumented); exceeding it can lock the account for days. Frequent forced refreshes drain the 12V battery.
- **Polling plan**: cached read every 15-30 min, forced refresh at most every 4 h (or never), and show "last updated" on screen.

Risks:

1. EU auth breaks periodically; an Oct 2026 `kia_uvo` issue reports fresh Hyundai EU logins failing on API 4.35.0.
2. Library maintainer no longer owns a Hyundai/Kia; fixes depend on community.
3. Adapter must fail soft: serve last known state with its timestamp when the API is down.

## Technical requirements

1. Server running Docker, reachable from both units over LAN (plain HTTP fine; HTTPS via reverse proxy optional).
2. 2.4 GHz Wi-Fi at both locations.
3. Per-source access: calendar ICS URL, P1/inverter on LAN, car API credentials.
4. USB-C cable and Chromium browser for the initial TRMNL flash. E1001 FW >= 1.6.7, E1003 FW >= 1.8.7.
5. Keep TRMNL OTA and early-release off; firmware updates managed manually.

## Open questions

1. Power: which meter/inverter (P1 dongle, solar)?
2. Calendar provider?
3. Mounting: wall with USB power, or battery only? Determines refresh interval.
4. What goes on which screen, per location?

## Sources

- Seeed wiki overview: https://wiki.seeedstudio.com/reterminal_e10xx_main_page/
- Seeed TRMNL guide: https://wiki.seeedstudio.com/reterminal_e10xx_trmnl
- Seeed ESPHome guide: https://wiki.seeedstudio.com/reterminal_e10xx_with_esphome
- E1003 getting started: https://wiki.seeedstudio.com/getting_started_with_reterminal_e1003/
- TRMNL BYOS: https://docs.trmnl.com/go/diy/byos
- TRMNL custom server: https://help.trmnl.com/en/articles/12263392-connect-your-device-to-terminus-byos
- TRMNL HA screenshot: https://help.trmnl.com/en/articles/13281388-home-assistant-screenshot
- TRMNL BYOD license: https://shop.trmnl.com/products/byod
- Puppet add-on: https://github.com/balloob/home-assistant-addons
- hyundai_kia_connect_api: https://github.com/Hyundai-Kia-Connect/hyundai_kia_connect_api
- Hyundai EU login issue: https://github.com/Hyundai-Kia-Connect/kia_uvo/issues/1927
- openHAB Bluelink (refresh/limits notes): https://www.openhab.org/addons/bindings/bluelink/
- LaraPaper: https://github.com/usetrmnl/larapaper
- SenseCraft HMI: https://sensecraft-hmi-docs.seeed.cc/en/overview/
