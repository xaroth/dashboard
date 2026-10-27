import { expect, test } from "bun:test";
import type { CarCharge, CarVehicle } from "../generated/queries";
import { chargeLabel, formatSeen, keyWarning, levels, openLabel, rangeLabel, vehicleName, warningLabels } from "./car";

const tz = "Europe/Amsterdam";
const now = "2026-10-16T10:30:00+02:00";

const vehicle = (v: Partial<CarVehicle>): CarVehicle => ({
  vin: "VIN", shortName: null, brand: "kia", nickname: "", keyExpiresAt: "0001-01-01T00:00:00Z", model: "EV6", modelCode: "CV",
  type: "EV", updatedAt: null, activeAt: null, ignitionOn: null, odometerKm: null, location: null, atHome: null, stateOfCharge: null,
  fuelLevel: null, ranges: [], charge: null, locked: null, open: [], climateOn: null, outsideTemperature: null, warnings: [],
  ...v,
});

test("names: short name, nickname, model", () => {
  expect(vehicleName(vehicle({ shortName: "Kia", nickname: "Auto" }))).toBe("Kia");
  expect(vehicleName(vehicle({ nickname: "Auto" }))).toBe("Auto");
  expect(vehicleName(vehicle({}))).toBe("EV6");
});

test("levels per car type", () => {
  expect(levels(vehicle({ stateOfCharge: 80 }))).toEqual([{ label: "Accu", percent: 80 }]);
  expect(levels(vehicle({ type: "PE", stateOfCharge: 30, fuelLevel: 70 })).map((l) => l.label)).toEqual(["Accu", "Tank"]);
  expect(levels(vehicle({ type: "HV", stateOfCharge: 55, fuelLevel: 40 })).map((l) => l.label)).toEqual(["Tank"]);
  expect(levels(vehicle({ type: "GN", fuelLevel: 40 })).map((l) => l.label)).toEqual(["Tank"]);
});

test("range labels", () => {
  expect(["EV", "ICE", "PHEV", "ev", "OTHER"].map(rangeLabel)).toEqual(["elektrisch", "brandstof", "totaal", "elektrisch", "other"]);
});

test("charge labels", () => {
  const c = (o: Partial<CarCharge>): CarCharge => ({
    pluggedIn: true, charging: true, status: "charging", targetAC: 80, targetDC: 90, remainingMinutes: 45, ...o,
  });
  // The minutes count from when the car reported them.
  const now = "2026-10-16T10:10:00+02:00";
  expect(chargeLabel(c({}), "2026-10-16T10:00:00+02:00", now, tz)).toBe("Laden tot 80%, vol om 10:45");
  expect(chargeLabel(c({ status: "fastCharging" }), "2026-10-16T10:00:00+02:00", now, tz)).toBe("Snelladen tot 90%, vol om 10:45");
  expect(chargeLabel(c({ remainingMinutes: 900 }), "2026-10-16T10:00:00+02:00", now, tz)).toBe("Laden tot 80%, vol om za 17 01:00");
  expect(chargeLabel(c({}), "2026-10-16T08:00:00+02:00", now, tz)).toBe("Laden tot 80%");
  expect(chargeLabel(c({ targetAC: null, remainingMinutes: null }), null, now, tz)).toBe("Laden");
  expect(chargeLabel(c({ charging: false }), null, now, tz)).toBe("Aangesloten");
  expect(chargeLabel(c({ charging: false, pluggedIn: false }), null, now, tz)).toBe("Niet aangesloten");
});

test("open parts and warnings in Dutch, unknown ones as named", () => {
  expect(openLabel(["trunk", "frontLeft"])).toBe("kofferbak, deur linksvoor");
  expect(openLabel([])).toBe("");
  expect(warningLabels(["washFluid", "brakeOil"])).toEqual(["ruitensproeiervloeistof", "brakeOil"]);
});

test("key expiry warns from 14 days", () => {
  expect(keyWarning(vehicle({}), now)).toBeNull();
  expect(keyWarning(vehicle({ keyExpiresAt: "2026-11-30T00:00:00Z" }), now)).toBeNull();
  expect(keyWarning(vehicle({ keyExpiresAt: "2026-10-21T00:00:00Z" }), now)).toBe("Sleutel verloopt over 5 d");
  expect(keyWarning(vehicle({ keyExpiresAt: "2026-10-16T00:00:00Z" }), now)).toBe("Sleutel verlopen");
});

test("last seen: time today, else day and time", () => {
  expect(formatSeen("2026-10-16T07:05:00+02:00", now, tz)).toBe("07:05");
  expect(formatSeen("2026-10-15T21:40:00+02:00", now, tz)).toBe("do 15 21:40");
  expect(formatSeen("2026-09-16T09:00:00+02:00", now, tz)).toBe("wo 16 09:00");
});
