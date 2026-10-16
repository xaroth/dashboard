# Research: reTerminal E1001 / E1003 dashboards

Dated entries record when an observation was made.

## Hardware

| | E1001 | E1003 |
| --- | --- | --- |
| Panel | 7.5" mono, 4-level gray | 10.3" mono, 16-level gray |
| Resolution | 800x480 (landscape) | 1872x1404 native (landscape; Seeed lists 1404x1872) |
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

- Server: TrueNAS with Docker, 10 Gbit uplink, reachable from the IoT network.
- Both units battery powered; no refreshes at night.
- If LaraPaper does not fit, build our own server rather than bend it.
- No Home Assistant: too much setup and upkeep for this use.

This rules out ESPHome+Puppet and the TRMNL HA add-on. ESPHome can run without HA (`online_image` from any URL), but then it only duplicates what TRMNL firmware already does, with firmware we maintain ourselves.

## Approach

TRMNL firmware on both units, pointed at our own BYOS server. No license, all local.

The device protocol is small: `/api/setup` (MAC in, `api_key` out), `/api/display` (returns `image_url`, `filename` as cache key, `refresh_rate` in seconds), the PNG itself, optional `/api/log`. Never return JSON `status: 500` from `/api/display`: the firmware treats it as a reset and wipes its credentials. Reference: `scripts/mock_server.py` in `usetrmnl/trmnl-firmware`.

Panel specs (TRMNL model API):

| Model | Size | Grays | PNG | Max size |
| --- | --- | --- | --- | --- |
| `seeed_e1001` | 800x480 | 4 | 2-bit | 750 KB (TRMNL model API says 90 KB; firmware with PSRAM allows 750 KB) |
| `seeed_e1003` | 1872x1404 | 16 | 4-bit | 750 KB |

LaraPaper supports both (it syncs models from that API) but is not used: two fixed screens don't justify a PHP app with a browser renderer.

Server: one Go service that fetches data, renders and serves the device protocol.

Rendering: TSX pages rendered by Takumi (Rust, no browser) through `takumi-js` in a resident Bun process; Go reduces the output to the panel's grays and encodes the PNG (see [design.md](design.md#rendering)). A spike with Takumi as a small CLI called per render (commit `8da478f`) measured 12 ms / 7.6 KB for E1001 and 35 ms / 34 KB for E1003, including process start.

## Data sources without Home Assistant

| Need | Source | Notes |
| --- | --- | --- |
| Weather | Open-Meteo or KNMI API | Free, no key (Open-Meteo) |
| Calendar | Google Workspace shared calendar | See below |
| Power usage | Smartstuff P1 dongle, DSMR-API firmware, local HTTP | No adapter needed, see below |
| Solar production | Hoymiles DTU, Modbus TCP (local) or S-Miles cloud | 16 panels, micro inverters; see below |
| Car status | Hyundai Bluelink EU cloud API | Kona 2024; needs adapter, see below |

Weather and calendar fit LaraPaper polling plugins directly. Car and power may need a small adapter on the server that exposes clean JSON.

## Refresh schedule

Both units on battery. TRMNL firmware sleeps for the `refresh_rate` (seconds) returned by `/api/display`, so the server controls the schedule:

- Daytime: fixed interval per device, e.g. 15-30 min.
- Night: return the seconds until the morning start time, so the device wakes once.
- Skip unchanged screens: same `filename` means no download and no redraw.

## Calendar: Google Workspace shared calendar

Two options:

1. **Secret iCal address** (calendar settings > Integrate calendar). Simplest: plain HTTPS GET of an `.ics`. Only shown to users who can manage sharing, and Workspace admins can disable it via Calendar external sharing settings. Anyone with the URL can read the calendar.
2. **Calendar API with a service account**. Share the calendar with the service account's email (read-only), call `events.list` with `singleEvents=true` and a time window. No user OAuth, no token expiry handling, survives password changes. Needs a Google Cloud project.

Preferred: iCal if available, service account otherwise.

## Power: Smartstuff P1 dongle (DSMR-API)

Verified 2026-10-15 at `192.168.1.20` (hostname `P1-Dongle-Pro`): firmware 4.14.4, no Basic Auth, local history enabled, 3-phase, gas present, MQTT off. Wi-Fi RSSI -74 dBm and 215 reboots logged; signal is weak.

`/api/v2/hist/*` values are cumulative meter readings per slot (ring buffer, `actSlot` = current); usage per period is the difference between slots.

Local HTTP JSON, no cloud, no adapter needed. Dashboard server polls it directly. Basic Auth applies if set in the dongle UI.

| Endpoint | Use |
| --- | --- |
| `/api/v2/sm/actual` | Live values: power delivered/returned, per-phase, meter readings, gas |
| `/api/v2/hist/hours`, `/days`, `/months` | History for today/week/month graphs; requires local storage enabled |
| `/api/v2/dash/live` | Combined live values incl. production/battery |
| `/api/v1/data` | HomeWizard-compatible, if reusing HomeWizard tooling |

Also available: MQTT push, WebSocket on `:81/ws` (max 3 clients), raw telegram on TCP port 82. Not needed for a dashboard that refreshes every few minutes.

Give the dongle a fixed IP or DHCP reservation.

## Solar: Hoymiles micro inverters

16 panels on 3x HMT-2250-6T (6 inputs each), DTU-Pro-S firmware V00.02.22, uploads to S-Miles Cloud. This matches the evcc-supported combination (HMT/HMS + DTU-Pro-S over Modbus TCP), so option 1 applies. The P1 dongle cannot read Hoymiles (its solar link supports SolarEdge, Enphase, SMA only), so the P1 only sees net import/export. Production must come from the DTU.

Options, in order of preference:

1. **Modbus TCP on the DTU** (local). Supported on DTU-Pro and DTU-Pro-S via the Ethernet port, TCP 502, unit ID 1. Data per inverter: current power, today's and total energy, temperature, alarms, grid voltage/frequency. Libraries: `hoymiles-modbus` (Python, pre-alpha, single maintainer), `ha-hoymiles-modbus-tcp` (register reference), evcc template `hoymiles-dtu-mb`. Cloud upload keeps working alongside.
2. **Native DTU protocol** (local, protobuf over TCP 10081). `hoymiles-wifi` (Python), tested with DTU-WLite, DTU-Pro, HMS-xxxW-2T. Option if the DTU has no Ethernet/Modbus. Newer DTU-Pro-S firmware reportedly encrypts it.
3. **S-Miles Cloud** (unofficial API). Works for any DTU, but cloud-dependent and coarse (DTU uploads every ~15 min).
4. **OpenDTU** (replace DTU with ESP32 + radio: nRF24 for HM series, CMT2300A for HMS). Local REST/MQTT, but drops S-Miles Cloud. Only if 1 and 2 fail.

Known issue (2026-10-15): one inverter reports Hardware Error Code 308, "INV overcurrent or overvoltage protection failure" (OpenDTU alarm table). 3xx codes are inverter hardware faults; if persistent, a warranty case through the installer. The dashboard should surface per-inverter alarms and an inverter producing much less than its siblings.

Consumption = P1 import - P1 export + solar production; needed for a self-consumption figure on screen.

DTU at `192.168.1.21`, wired Ethernet. Read-only test script: `tools/dtu_read.py <host>`.

Register map (from `hoymiles-modbus`): DTU serial at 0x2000 (3 regs). Per inverter port: 20 holding regs at `0x1000 + i*40`; serial (6 B), port, PV V/A, grid V/Hz, PV W, today Wh, total Wh, temp, status, alarm, link. Plant totals are sums over linked ports. Some DTUs send a wrong byte-count in responses; `hoymiles-modbus` patches it.

### Modbus status (2026-10-15)

TCP 502 accepts connections, but FC03 reads get no response on any unit ID tried. Same client works against the P1 dongle's Modbus server, so the client and network path are fine; the DTU's Modbus service is not answering. TCP 80 and 10081 closed.

Likely causes, most likely first:

1. **Firmware V00.02.22**: Photovoltaikforum (Feb 2024) reports exactly this on DTU-Pro-S V00.02.22: every Modbus TCP query times out. V00.02.23 reported the same (ioBroker, 2024). Hoymiles support reflashed affected units to V00.02.19, which restored Modbus. DTU-Pro firmware has since moved to V00.07.x (V00.07.04 is the minimum for `ha-hoymiles-dtupro`); whether a V00.07 build exists for the DTU-Pro-S is unconfirmed. Updates are offered through S-Miles; rollback only via Hoymiles support.
2. **RS-485 port config**: in the S-Miles Installer app, Toolkit > DTU Information > RS-485 Port Config. "Export control" blocks Modbus TCP; "Remote control" enables it. Note the Modbus address shown there.
3. **Address mismatch**: ruled out; a sweep of unit IDs 0-255 got no response on any.

### DTU firmware history

Hoymiles publishes no changelog. Installers update remotely: S-Miles Cloud web portal > plant > device list > DTU > Device Maintenance > Upgrade, pick target version, confirm. Installer accounts can upgrade DTUs but not microinverters (needs Hoymiles support). Hoymiles' Aug 2026 security rollout notifies owners in the S-Miles Home app and applies within ten working days after the owner authorizes. Reconstructed from Photovoltaikforum and ioBroker reports; same version line covers DTU-Pro and DTU-Pro-S:

| Version | Seen | Reports |
| --- | --- | --- |
| V00.02.19 | 2023 | Fixed DTU crashes from Modbus polling; Modbus works. Support rolls units back to this. |
| V00.02.22 | Jan 2024 | Modbus TCP queries time out (multiple users, DTU-Pro-S H09.06.01). |
| V00.02.23 | 2024 | Modbus reported dead by some, working for others; favored by HM-1500 owners. |
| V00.05.02 | 2025 | Energy spikes; HM-1500 issues. |
| V00.06.05 | Aug 2025 | HM-1500 inverters switch off; some roll back to 2.23. |
| V00.07.02 | Jan 2026 | No visible change; DTU crashes 2-4x/day under frequent Modbus polling (support: poll less often). |
| V00.07.04 | 2026 | Minimum for `ha-hoymiles-dtupro`. |

Our DTU runs V00.02.22, from January 2024. Polling no more than every 60 s is advisable on any version.

No installer access (plant registered by the installer). Workarounds:

1. **S-Miles Cloud with the owner account** (preferred). Reverse-engineered API (`/iam/pub/3/auth/login`, v3 login); reference implementation: `homeassistant-hoymiles-cloud` (Python). Gives PV power, today/total energy, per-channel PV. Cloud lags the DTU upload interval, which is fine for screens refreshing every 15-30 min. Risk: unofficial, may break.
2. **Installer** changes RS-485 port config to Remote control and updates firmware, or transfers installer rights.
3. **Hoymiles support** as owner: report Modbus TCP timeouts on DTU-Pro-S V00.02.22, ask for a working firmware (they have reflashed units remotely before).
4. **DIY account** in the S-Miles Installer app. Hoymiles FAQ: DIY homeowners may register in the Installer app (beta; NL supported). A DIY account only manages plants it owns; the DTU is bound to the installer's plant. Plant transfers between accounts go through Hoymiles technical support. Route: register DIY account, try Toolkit local connection at the DTU; if refused, ask support to transfer the plant to the DIY account. Gives control over RS-485 config and firmware updates; the installer loses access, which may matter for warranty service.

OpenDTU is a weak fallback: drops S-Miles, reported gaps for HMT 3-phase data, and Hoymiles' Aug 2026 inverter security firmware blocks OpenDTU/AhoyDTU on updated inverters.

Plan: build against S-Miles Cloud behind the same adapter interface as Modbus; switch to Modbus when it answers.

## Car: Hyundai Kona Electric 2024 (Bluelink EU)

Fields that matter: SoC, range, plugged in, charging, time to full, target SoC, locks/doors, climate, last update time.

### Official: Pleos Vehicle Data API

Hyundai Motor Group's EU Data Act API, read-only, Europe only (NL included).

- Private users: sign up at Pleos Playground, account menu > My Vehicle Data API > Hyundai tab, accept terms, sign in with the Bluelink account. Issues a Client ID/Secret immediately, no approval, one key per brand.
- Auth: `POST https://api.pleos.ai/v1/auth/personal-token` with `clientId`, `clientSecret`, header `Brand: hyundai`; returns a bearer token. Re-issue on expiry.
- Vehicles: `GET /v1/vehicles/consent` lists VINs.
- Data: `/v1/vehicles/{vin}/batteries` (plug, charging, SoC, time to full), `/powertrains` (fuel level, range), `/locations`, `/driving` (ignition, odometer, park), `/status` (doors, trunk, warnings, climate).
- Docs do not state rate limits or data freshness.
- Compatibility table (Oct 2026) lists ELANTRA, INSTER, IONIQ 5/6, IONIQ, and "KONA Electric (HEV)" with every field marked N. The 2024 Kona (SX2) is not listed. Issuing a key and calling it is the only way to know what returns data.

### Unofficial: hyundai_kia_connect_api

`hyundai_kia_connect_api` (Python, the library behind the HA `kia_uvo` integration) talks to the Bluelink app backend.

- **Auth**: EU username/password login supported directly since v4.12.0. Fallback: generate a refresh token via a browser tool (KiaHyundaiToken in the library repo, or bluelink-refresh-token); valid ~180 days.
- **Data**: SoC, range, charging state, locks/doors, climate, odometer, location, last update time, trip info (EU).
- **Cached vs forced**: cached reads return the last state the car pushed to the cloud (car pushes on events and at every 10% SoC). Forced refresh wakes the car via the modem.
- **Limits**: EU API is daily rate-limited (cap undocumented); exceeding it can lock the account for days. Frequent forced refreshes drain the 12V battery.
- **Polling plan**: cached read every 15-30 min, forced refresh at most every 4 h (or never), and show "last updated" on screen.

Risks: EU auth breaks periodically (Oct 2026 `kia_uvo` issue reports fresh Hyundai EU logins failing on API 4.35.0); maintainer no longer owns a Hyundai/Kia.

### Plan

1. Issue a Pleos key and test against the Kona. If fields come back, use it: official, stable auth, no 12V or lockout risk.
2. Otherwise use the unofficial library.
3. Either way, the adapter serves last known state with its timestamp when the upstream is down.

## Technical requirements

1. Server running Docker, reachable from both units over LAN (plain HTTP fine; HTTPS via reverse proxy optional).
2. 2.4 GHz Wi-Fi at both locations.
3. Per-source access: calendar ICS URL, P1 dongle on LAN with local history enabled, Bluelink credentials.
4. USB-C cable and Chromium browser for the initial TRMNL flash. E1001 FW >= 1.6.7, E1003 FW >= 1.8.7.
5. Keep TRMNL OTA and early-release off; firmware updates managed manually.

## Open questions

1. DTU Modbus: via installer or Hoymiles support (see Solar workarounds).
2. iCal address of the shared calendar (likely available).
3. Does the Pleos API return data for the Kona Electric 2024?
4. Content, layout and placement per screen.

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
- Smartstuff DSMR-API endpoints: https://docs.smart-stuff.nl/dsmr-api/geavanceerd/api-overzicht
- hoymiles-modbus: https://pypi.org/project/hoymiles-modbus/
- ha-hoymiles-modbus-tcp: https://github.com/wil-lem/ha-hoymiles-modbus-tcp
- evcc Hoymiles DTU Modbus: https://docs.evcc.io/en/meters/hoymiles-hmt-hms-series-with-dtu-pro-s-modbus-tcp
- hoymiles-wifi: https://pypi.org/project/hoymiles-wifi/
- Pleos Vehicle Data API: https://document.pleos.ai/en/api-reference/vehicle-data-api/intro
- Pleos API compatibility: https://document.pleos.ai/en/api-reference/vehicle-data-api/api-compatibility
- Hyundai data rights (EU): https://connected-mobility.hyundai.com/data-rights-en
- Google Calendar API events.list: https://developers.google.com/workspace/calendar/api/v3/reference/events/list
- ioBroker DTU-Pro-S Modbus thread: https://forum.iobroker.net/topic/74880/hoymiles-dtu-pro-s-modbus
- ha-hoymiles-dtupro: https://github.com/netnic0/ha-hoymiles-dtupro
- Photovoltaikforum DTU firmware 2.22: https://www.photovoltaikforum.com/thread/221261-neues-firmware-update-2-22-f%C3%BCr-dtu-pro-erh%C3%A4ltlich/
- heise, Hoymiles security update: https://www.heise.de/en/news/PV-inverters-Hoymiles-releases-security-update-11436678.html
- homeassistant-hoymiles-cloud: https://github.com/Philra94/homeassistant-hoymiles-cloud
- Hoymiles FAQ (DIY accounts): https://www.hoymiles.com/us/faq_4.html
- Photovoltaikforum DTU V00.07.02: https://www.photovoltaikforum.com/thread/256328-homiles-dtu-pro-v00-07-02-firmware/
- Photovoltaikforum DTU 6.05: https://www.photovoltaikforum.com/thread/251113-neues-firmware-update-6-05-f%C3%BCr-dtu-pro-erh%C3%A4ltlich/
- S-Miles Cloud user guide: https://static.hoymiles.com/cfs/doc/S-miles%20Cloud%20User%20Guide%20V.06.pdf
- OpenDTU alarm codes: https://github.com/tbnobody/OpenDTU/blob/master/lib/Hoymiles/src/parser/AlarmLogParser.cpp
- LaraPaper: https://github.com/usetrmnl/larapaper
- SenseCraft HMI: https://sensecraft-hmi-docs.seeed.cc/en/overview/
