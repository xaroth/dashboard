import { expect, test } from "bun:test";
import type { CarVehicle, EveonlineSkillsCharacter } from "../generated/queries";
import { carsSummary, combine, nextEvent, soonestQueue } from "./home";

test("combined status: stale if any is, oldest fetch, first error", () => {
  const fresh = { fetchedAt: "2026-10-16T10:25:00+02:00", stale: false, error: null };
  const old = { fetchedAt: "2026-10-16T08:00:00+02:00", stale: true, error: "timeout" };
  const none = { fetchedAt: null, stale: true, error: "refused" };
  expect(combine(fresh)).toEqual(fresh);
  expect(combine(fresh, old, none)).toEqual({ fetchedAt: old.fetchedAt, stale: true, error: "timeout" });
  expect(combine(none)).toEqual(none);
});

const car = (v: Partial<CarVehicle>): CarVehicle => ({
  vin: "VIN1", shortName: null, brand: "kia", nickname: "", keyExpiresAt: "", model: "EV6", modelCode: "", type: "EV",
  updatedAt: null, activeAt: null, ignitionOn: null, odometerKm: null, location: null, stateOfCharge: null, fuelLevel: null,
  ranges: [], charge: null, locked: null, open: [], climateOn: null, outsideTemperature: null, warnings: [], ...v,
});

const now = "2026-10-16T10:30:00+02:00";
const tz = "Europe/Amsterdam";
const charge = { pluggedIn: true, charging: true, status: "charging", targetAC: 80, targetDC: 90, remainingMinutes: 60 };

test("one car: levels, longest range, charging", () => {
  const ev = car({ shortName: "ev", stateOfCharge: 64.4, ranges: [{ type: "EV", km: 255 }], charge, updatedAt: now });
  expect(carsSummary([ev], now, tz)).toEqual({ label: "ev", value: "64% · 255 km", detail: "Laden tot 80%, vol om 11:30" });
  const parked = car({ type: "GN", fuelLevel: 18, ranges: [{ type: "ICE", km: 95 }] });
  expect(carsSummary([parked], now, tz)).toEqual({ label: "EV6", value: "18% · 95 km", detail: null });
  expect(carsSummary([car({})], now, tz).value).toBeNull();
});

test("several cars share a row; plugged-in ones say how they charge", () => {
  const ev = car({ shortName: "ev", stateOfCharge: 64, charge, updatedAt: now });
  const phev = car({ shortName: "phev", type: "PE", stateOfCharge: 35, fuelLevel: 72, charge: { ...charge, charging: false } });
  const off = car({ shortName: "off", stateOfCharge: 90, charge: { ...charge, pluggedIn: false, charging: false } });
  const unknown = car({ shortName: "x" });
  expect(carsSummary([ev, phev, off, unknown], now, tz)).toEqual({
    label: "Auto's",
    value: "ev 64% · phev 35%/72% · off 90% · x",
    detail: "ev: Laden tot 80%, vol om 11:30 · phev: Aangesloten",
  });
});

const event = (title: string, start: string, end: string, allDay = false) => ({ calendar: "c", title, start, end, allDay, location: "" });

test("next event: the first yet to start today or tomorrow, else one under way", () => {
  const holiday = event("Vakantie", "2026-10-10T00:00:00+02:00", "2026-10-19T00:00:00+02:00", true);
  const dentist = event("Tandarts", "2026-10-16T14:00:00+02:00", "2026-10-16T14:45:00+02:00");
  const swim = event("Zwemles", "2026-10-17T11:30:00+02:00", "2026-10-17T12:15:00+02:00");
  const later = event("Etentje", "2026-10-18T19:00:00+02:00", "2026-10-18T22:00:00+02:00");
  expect(nextEvent([holiday, dentist, swim, later], now, tz)).toEqual({ label: "Agenda", value: "Tandarts", detail: "Vandaag 14:00 - 14:45 · nog 2" });
  expect(nextEvent([holiday, later], now, tz)).toEqual({ label: "Agenda", value: "Vakantie", detail: "Vandaag t/m zo 18 okt" });
  expect(nextEvent([swim], "2026-10-16T23:30:00+02:00", tz)?.detail).toBe("Morgen 11:30 - 12:15");
  expect(nextEvent([later], now, tz)).toBeNull();
  expect(nextEvent([{ ...dentist, title: "" }], now, tz)?.value).toBe("(geen titel)");
});

const pilot = (id: number, c: Partial<EveonlineSkillsCharacter>): EveonlineSkillsCharacter => ({
  id, name: `P${id}`, paused: false, ends: null, remaining: 0, skills: [], totalSp: 0, unallocatedSp: 0,
  fetchedAt: "2026-10-16T08:00:00Z", error: "", ...c,
});

test("soonest queue among characters with data", () => {
  expect(soonestQueue([])).toEqual({ first: null, failed: 0 });
  const later = pilot(1, { ends: "2026-10-18T00:00:00Z" });
  const sooner = pilot(2, { ends: "2026-10-17T00:00:00Z", error: "timeout" });
  const never = pilot(3, { fetchedAt: null, error: "token expired" });
  expect(soonestQueue([later, sooner, never])).toEqual({ first: sooner, failed: 2 });
  expect(soonestQueue([never])).toEqual({ first: null, failed: 1 });
  const empty = pilot(4, {});
  expect(soonestQueue([later, empty]).first).toBe(empty);
});
