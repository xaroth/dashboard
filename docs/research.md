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

## Requirements per data source

| Need | Likely source | Notes |
| --- | --- | --- |
| Car status | HA integration for the car brand | Brand TBD; most have HA integrations (cloud APIs) |
| Weather | HA weather integration, or TRMNL plugin | |
| Calendar | HA calendar (Google/CalDAV), or TRMNL plugin | |
| Power usage | HA energy (P1 meter / DSMR, solar inverter) | |

All four are easiest through one hub (Home Assistant). Only weather and calendar work without it.

## Recommendation

Use Home Assistant as the single data and rendering hub, then pick the device protocol:

- **ESPHome + Puppet** (preferred if HA is already running): local, free, devices show up in HA, buttons usable, one Lovelace dashboard per device sized to its resolution.
- **TRMNL BYOS** (alternative): device firmware maintained by TRMNL, simple pull-image protocol, server renders screens; HA data fed via screenshot or API. More moving parts (extra server).

Avoid stock Seeedash and hosted TRMNL: neither reaches LAN data without pushing it to a cloud.

## Technical requirements

1. Home Assistant instance reachable on the LAN (add-ons available, or Docker equivalents).
2. 2.4 GHz Wi-Fi at both locations.
3. HA integrations for car, weather, calendar, energy meter.
4. Puppet add-on (ESPHome path) or a BYOS server container (TRMNL path).
5. ESPHome >= 2026.7.0; USB-C cable and Chromium browser for initial flash.

## Open questions

1. Is Home Assistant already running, and how (HAOS, Docker)?
2. Car brand/model?
3. Power source: P1 meter, solar, other?
4. Mounting: wall with USB power, or battery only? Determines refresh interval.
5. What goes on which screen, per location?

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
- SenseCraft HMI: https://sensecraft-hmi-docs.seeed.cc/en/overview/
