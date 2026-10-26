import type { Result } from "../pages/generated/props";
import type { Queries } from "../pages/generated/queries";
import type { QueryName } from "../pages/lib/page";
import type { Page, RenderProps } from "./bundle";

// Sample data for every page, shared by the page tests and the preview.

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
export const fixtures: { [K in QueryName]: NonNullable<Queries[K]["result"]> } = {
  "calendar.upcoming": {
    events: [
      { calendar: "family", title: "Herfstvakantie", start: "2026-10-10T00:00:00+02:00", end: "2026-10-19T00:00:00+02:00", allDay: true, location: "" },
      { calendar: "work", title: "Standup", start: "2026-10-16T09:30:00+02:00", end: "2026-10-16T09:45:00+02:00", allDay: false, location: "Microsoft Teams" },
      { calendar: "family", title: "Tandarts", start: "2026-10-16T14:00:00+02:00", end: "2026-10-16T14:45:00+02:00", allDay: false, location: "Hoofdstraat 1, Utrecht" },
      { calendar: "family", title: "Zwemles (diploma)", start: "2026-10-17T11:30:00+02:00", end: "2026-10-17T12:15:00+02:00", allDay: false, location: "Mirandabad" },
      { calendar: "family", title: "Verjaardag oma", start: "2026-10-18T00:00:00+02:00", end: "2026-10-19T00:00:00+02:00", allDay: true, location: "" },
      { calendar: "family", title: "Etentje", start: "2026-10-18T19:00:00+02:00", end: "2026-10-18T22:00:00+02:00", allDay: false, location: "" },
      { calendar: "work", title: "Standup", start: "2026-10-19T09:30:00+02:00", end: "2026-10-19T09:45:00+02:00", allDay: false, location: "Microsoft Teams" },
      { calendar: "work", title: "Sprint review", start: "2026-10-19T15:00:00+02:00", end: "2026-10-19T16:00:00+02:00", allDay: false, location: "" },
      { calendar: "work", title: "Nachtdienst", start: "2026-10-20T22:00:00+02:00", end: "2026-10-21T07:00:00+02:00", allDay: false, location: "" },
      { calendar: "work", title: "Standup", start: "2026-10-21T09:30:00+02:00", end: "2026-10-21T09:45:00+02:00", allDay: false, location: "Microsoft Teams" },
    ],
    calendars: [
      { name: "family", fetchedAt, error: "" },
      { name: "work", fetchedAt: "2026-10-16T09:10:00+02:00", error: "feed: 500 Internal Server Error" },
    ],
  },
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
  "rss.items": {
    name: "news",
    title: "Voorbeeldnieuws",
    link: "https://news.example.com/",
    fetchedAt,
    error: "",
    items: [
      { title: "Kabinet presenteert plannen voor woningbouw", published: "2026-10-16T09:45:00+02:00", summary: "Het kabinet wil de komende tien jaar een miljoen woningen bouwen." },
      {
        title: "Storm verwacht in het noorden van het land, code oranje voor de Waddeneilanden en de kust van Groningen en Friesland",
        published: "2026-10-16T08:10:00+02:00",
        summary: "Het KNMI waarschuwt voor windstoten tot 110 km/u. Veerdiensten varen mogelijk niet, en het advies is om niet onnodig de weg op te gaan.",
      },
      { title: "Spoorstoring tussen Utrecht en Amersfoort opgelost", published: "2026-10-16T07:30:00+02:00", summary: "" },
      { title: "Nieuwe tentoonstelling in het Rijksmuseum", published: "2026-10-15T21:00:00+02:00", summary: "Ruim honderd werken uit de Gouden Eeuw." },
      { title: "Ajax wint thuis van PSV", published: "2026-10-15T20:15:00+02:00", summary: "Een doelpunt in de slotminuut besliste het duel." },
      { title: "Benzineprijs daalt voor derde week op rij", published: "2026-10-15T16:00:00+02:00", summary: "Een liter Euro95 kost gemiddeld 1,89 euro." },
      { title: "Bericht zonder datum", published: null, summary: "Sommige feeds geven geen tijd." },
      { title: "Universiteit opent nieuw onderzoekscentrum", published: "2026-10-15T11:00:00+02:00", summary: "Het centrum richt zich op klimaatadaptatie." },
    ].map((item, i) => ({ ...item, link: `https://news.example.com/${i}` })),
  },
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

export const variants: Record<string, (value: unknown) => Result<unknown>> = {
  missing: () => ({ value: null, fetchedAt: null, stale: true, error: "connection refused" }),
  stale: (value) => ({ value, fetchedAt, stale: true, error: "timeout" }),
  populated: (value) => ({ value, fetchedAt, stale: false, error: null }),
};

export const viewports = [
  { width: 800, height: 480, portrait: false },
  { width: 480, height: 800, portrait: true },
];

export function props(page: Page, result: (value: unknown) => Result<unknown>, viewport: RenderProps["viewport"]): RenderProps {
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
