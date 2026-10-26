import type { CarCharge, CarVehicle } from "../generated/queries";
import { formatDayShort, formatDayTime, formatNumber, formatTime } from "./format";

/** The short name from the admin page, else the app's nickname, else the model. */
export function vehicleName(v: CarVehicle): string {
  return v.shortName || v.nickname || v.model;
}

export type Level = { label: string; percent: number };

/** Battery and tank levels a car reports, in that order. */
export function levels(v: CarVehicle): Level[] {
  const out: Level[] = [];
  // A hybrid (HV) charges its own battery; its level says nothing to act on.
  if (v.stateOfCharge != null && v.type !== "HV" && v.type !== "GN") out.push({ label: "Accu", percent: v.stateOfCharge });
  if (v.fuelLevel != null) out.push({ label: "Tank", percent: v.fuelLevel });
  return out;
}

const rangeLabels: Record<string, string> = {
  EV: "elektrisch",
  ICE: "brandstof",
  HEV: "hybride",
  PHEV: "totaal",
  FCEV: "waterstof",
};

export function rangeLabel(type: string): string {
  return rangeLabels[type.toUpperCase()] ?? type.toLowerCase();
}

/** 320 km */
export function formatKm(km: number): string {
  return `${formatNumber(km, 0)} km`;
}

function fastCharging(c: CarCharge): boolean {
  return /fast|quick/i.test(c.status);
}

/** When charging ends: the reported minutes count from when the car sent them, not from now. */
export function chargeDone(c: CarCharge, updatedAt: string | null): string | null {
  if (!c.charging || !c.remainingMinutes || !updatedAt) return null;
  return new Date(Date.parse(updatedAt) + c.remainingMinutes * 60_000).toISOString();
}

/** "Snelladen tot 80%, vol om 11:15", "Aangesloten", "Niet aangesloten". */
export function chargeLabel(c: CarCharge, updatedAt: string | null, timezone: string): string {
  if (!c.charging) return c.pluggedIn ? "Aangesloten" : "Niet aangesloten";
  const target = fastCharging(c) ? c.targetDC : c.targetAC;
  const done = chargeDone(c, updatedAt);
  return `${fastCharging(c) ? "Snelladen" : "Laden"}${target != null ? ` tot ${target}%` : ""}${done ? `, vol om ${formatTime(done, timezone)}` : ""}`;
}

const openLabels: Record<string, string> = {
  frontLeft: "deur linksvoor",
  frontRight: "deur rechtsvoor",
  rearLeft: "deur linksachter",
  rearRight: "deur rechtsachter",
  hood: "motorkap",
  trunk: "kofferbak",
  sunroof: "schuifdak",
};

/** "kofferbak, deur linksvoor"; empty when everything is closed. */
export function openLabel(open: string[]): string {
  return open.map((o) => openLabels[o] ?? o).join(", ");
}

const warningNames: Record<string, string> = {
  washFluid: "ruitensproeiervloeistof",
  tirePressureLow: "bandenspanning laag",
};

/** Pleos warning names in Dutch where known, else as Pleos names them. */
export function warningLabels(warnings: string[]): string[] {
  return warnings.map((w) => warningNames[w] ?? w);
}

/** "Sleutel verloopt over 5 d" from 14 days before the API key expires; keys are reissued by hand. */
export function keyWarning(v: CarVehicle, now: string): string | null {
  const expires = Date.parse(v.keyExpiresAt);
  // Go's zero time: no expiry known.
  if (!(expires > 0)) return null;
  const ms = expires - Date.parse(now);
  if (ms <= 0) return "Sleutel verlopen";
  const days = Math.ceil(ms / 86_400_000);
  return days <= 14 ? `Sleutel verloopt over ${days} d` : null;
}

/** "10:25" on the day of now, else "vr 16 07:30". */
export function formatSeen(iso: string, now: string, timezone: string): string {
  const sameDay = formatDayShort(iso, timezone) === formatDayShort(now, timezone) && Date.parse(now) - Date.parse(iso) < 86_400_000;
  return sameDay ? formatTime(iso, timezone) : formatDayTime(iso, timezone);
}
