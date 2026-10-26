import { Renderer } from "@takumi-rs/core";
import { describe, expect, test } from "bun:test";
import { Glob } from "bun";
import { join } from "node:path";
import type { Result } from "../pages/generated/props";
import type { Queries } from "../pages/generated/queries";
import type { QueryName } from "../pages/lib/page";
import { bundlePages, type Page, type RenderProps } from "./bundle";
import { renderPage } from "./render";

const pagesDir = join(import.meta.dir, "../pages");
const now = "2026-10-16T10:30:00+02:00";
const fetchedAt = "2026-10-16T10:25:00+02:00";

// Points every 15 minutes over the last 24 hours, with a gap.
function series(value: (i: number) => number) {
  const start = Date.parse(now) - 24 * 60 * 60 * 1000;
  return Array.from({ length: 96 }, (_, i) => ({
    t: new Date(start + i * 15 * 60 * 1000).toISOString(),
    v: i >= 40 && i < 44 ? null : value(i),
  }));
}

const skill = (id: number, level: number, finish: string | null) => ({ id, name: `Skill ${id}`, level, start: null, finish });

// A populated result for every query: a new query fails to type-check until it has one.
const fixtures: { [K in QueryName]: NonNullable<Queries[K]["result"]> } = {
  "car.vehicle": {
    vin: "KMH0000000000000",
    shortName: "Ioniq",
    brand: "hyundai",
    nickname: "Auto",
    keyExpiresAt: "2027-01-01T00:00:00Z",
    model: "IONIQ 5",
    modelCode: "NE",
    type: "EV",
    updatedAt: fetchedAt,
    activeAt: null,
    ignitionOn: false,
    odometerKm: 12345,
    location: { latitude: 52.1, longitude: 5.1 },
    stateOfCharge: 80,
    fuelLevel: null,
    ranges: [{ type: "ev", km: 320 }],
    charge: { pluggedIn: true, charging: true, status: "charging", targetAC: 80, targetDC: 80, remainingMinutes: 45 },
    locked: true,
    open: [],
    climateOn: false,
    outsideTemperature: 12,
    warnings: [],
  },
  "car.vehicles": [],
  "eveonline_skills.queue": [
    {
      id: 1,
      name: "Pilot One",
      paused: false,
      ends: "2026-10-16T20:00:00Z",
      remaining: 34200,
      skills: [skill(3300, 4, "2026-10-16T12:00:00Z"), skill(3301, 5, "2026-10-16T20:00:00Z")],
      totalSp: 52_000_000,
      unallocatedSp: 250_000,
      fetchedAt,
      error: "",
    },
    { id: 2, name: "Pilot Two", paused: true, ends: null, remaining: 0, skills: [skill(3302, 3, null)], totalSp: 9_000_000, unallocatedSp: 0, fetchedAt, error: "" },
    { id: 3, name: "Pilot Three", paused: false, ends: null, remaining: 0, skills: [], totalSp: 1_000_000, unallocatedSp: 0, fetchedAt: null, error: "token expired" },
  ],
  "eveonline_status.history": series((i) => 20000 + i * 10),
  "eveonline_status.server": { online: true, players: 23456, serverVersion: "2876543", startTime: "2026-10-16T11:05:00Z", vip: false },
  "p1.actual": {
    measuredAt: fetchedAt,
    voltage: { l1: 231.2, l2: 229.8, l3: null },
    flowKw: -1.234,
    today: { from: "2026-10-16T00:00:05+02:00", to: fetchedAt, takenKwh: 4.2, returnedKwh: 6.8, gasM3: 1.23, gasFrom: "2026-10-16T00:00:00+02:00", gasTo: fetchedAt },
    yesterday: { from: "2026-10-15T06:30:00+02:00", to: "2026-10-16T00:00:05+02:00", takenKwh: 9.1, returnedKwh: 3.4, gasM3: null, gasFrom: null, gasTo: null },
  },
  "p1.flow": series((i) => Math.sin(i / 8) * 2),
  "solar.power": series((i) => Math.max(0, Math.sin((i - 24) / 15) * 3000)),
  "solar.production": {
    powerW: 2345,
    panels: Array.from({ length: 17 }, (_, i) => ({ inverter: `inv${i >> 2}`, port: i % 4, powerW: 50 + i * 10 })),
  },
  "weather.forecast": {
    location: { name: "Utrecht", country: "NL", latitude: 52.09, longitude: 5.12, timezone: "Europe/Amsterdam" },
    current: { time: fetchedAt, temperature: 12.4, apparent: 10.1, weatherCode: 3, windSpeed: 18, windDirection: 225, precipitation: 0, isDay: true },
    hourly: Array.from({ length: 24 }, (_, i) => ({
      time: new Date(Date.parse(now) + i * 60 * 60 * 1000).toISOString(),
      temperature: 10 + Math.sin(i / 4) * 4,
      precipitationProbability: i % 5 === 0 ? null : (i * 7) % 100,
      precipitation: 0.1 * (i % 3),
      weatherCode: 61,
    })),
    daily: ["2026-10-16", "2026-10-17", "2026-10-18"].map((date, i) => ({
      date,
      weatherCode: [3, 61, 0][i]!,
      temperatureMax: 14 + i,
      temperatureMin: 6 + i,
      precipitationSum: 2.5 * i,
      precipitationProbabilityMax: i === 2 ? null : 40,
      sunrise: `${date}T07:58:00+02:00`,
      sunset: `${date}T18:40:00+02:00`,
    })),
  },
};

const variants: Record<string, (value: unknown) => Result<unknown>> = {
  missing: () => ({ value: null, fetchedAt: null, stale: true, error: "connection refused" }),
  stale: (value) => ({ value, fetchedAt, stale: true, error: "timeout" }),
  populated: (value) => ({ value, fetchedAt, stale: false, error: null }),
};

const viewports = [
  { width: 800, height: 480, portrait: false },
  { width: 480, height: 800, portrait: true },
];

function props(page: Page, result: (value: unknown) => Result<unknown>, viewport: RenderProps["viewport"]): RenderProps {
  const data: Record<string, Result<unknown>> = {};
  for (const [key, q] of Object.entries(page.data)) {
    const name = `${q.module}.${q.query}`;
    if (!(name in fixtures)) throw new Error(`${page.name}: no fixture for ${name}`);
    data[key] = result(fixtures[name as QueryName]);
  }
  return {
    data,
    device: {
      name: "test",
      model: "reterminal_e1001",
      rotation: viewport.portrait ? 90 : 0,
      wake: { source: "timer", battery: 4.01, rssi: -61, firmware: "1.6.2", at: now, usb: false, powerSource: "header" },
    },
    viewport,
    system: { staleModules: ["p1"] },
    now,
    timezone: "Europe/Amsterdam",
  };
}

const pages = await bundlePages(pagesDir);
const renderer = new Renderer();

describe("every page renders", () => {
  for (const page of pages.values()) {
    for (const [variant, result] of Object.entries(variants)) {
      for (const viewport of viewports) {
        test(`${page.name} ${variant} ${viewport.width}x${viewport.height}`, async () => {
          const png = await renderPage(renderer, page, props(page, result, viewport));
          expect([...png.slice(1, 4)]).toEqual([80, 78, 71]);
          const view = new DataView(png.buffer, png.byteOffset);
          expect([view.getUint32(16), view.getUint32(20)]).toEqual([viewport.width, viewport.height]);
        });
      }
    }
  }
});

test("every page is bundled", () => {
  const files = [...new Glob("layouts/*/*.tsx").scanSync({ cwd: pagesDir })];
  expect(pages.size).toBe(files.length);
});

// CSS modules are typed loosely (css.d.ts); a misspelled class would be undefined and silently unstyled.
test("every class a component uses is in its CSS module", async () => {
  const missing: string[] = [];
  for (const file of new Glob("**/*.tsx").scanSync({ cwd: pagesDir })) {
    const src = await Bun.file(join(pagesDir, file)).text();
    const imp = src.match(/import (\w+) from "(\.[^"]+\.module\.css)"/);
    if (!imp) continue;
    const [, ident, cssPath] = imp;
    const css = await Bun.file(join(pagesDir, file, "..", cssPath!)).text();
    const defined = new Set([...css.matchAll(/\.([A-Za-z_][\w-]*)/g)].map((m) => m[1]));
    for (const [, cls] of src.matchAll(new RegExp(`\\b${ident}\\.(\\w+)`, "g"))) {
      if (!defined.has(cls)) missing.push(`${file}: ${cls}`);
    }
  }
  expect(missing).toEqual([]);
});
