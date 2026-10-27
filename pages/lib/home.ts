import type { CalendarEvent, CarVehicle, EveonlineSkillsCharacter } from "../generated/queries";
import { addDays, agendaDays, dayLabel, eventTime, localDate } from "./calendar";
import { chargeDone, chargeLabel, chargeTarget, formatKm, levels, vehicleName, type Level } from "./car";
import { sortQueues } from "./eveonline";
import { formatTime } from "./format";
import type { Result } from "./page";

export type Status = Pick<Result<unknown>, "fetchedAt" | "stale" | "error">;

/** One status for a row built from several results: stale when any is, with the oldest fetch and the first error. */
export function combine(...results: Status[]): Status {
  const fetched = results.flatMap((r) => (r.fetchedAt ? [r.fetchedAt] : [])).sort((a, b) => Date.parse(a) - Date.parse(b));
  return {
    fetchedAt: fetched[0] ?? null,
    stale: results.some((r) => r.stale),
    error: results.find((r) => r.error)?.error ?? null,
  };
}

export type Summary = { label: string; value: string | null; detail: string | null };

const percent = (v: CarVehicle) => levels(v).map((l) => `${Math.round(l.percent)}%`);

/**
 * One car: its levels and longest range, and how it charges. Several cars
 * share one row, each with its levels, and list the ones plugged in.
 */
export function carsSummary(cars: CarVehicle[], now: string, timezone: string): Summary {
  const plugged = cars.flatMap((v) =>
    v.charge && (v.charge.pluggedIn || v.charge.charging) ? [{ v, label: chargeLabel(v.charge, v.updatedAt, now, timezone) }] : [],
  );
  if (cars.length === 1) {
    const v = cars[0]!;
    const range = v.ranges.length ? formatKm(Math.max(...v.ranges.map((r) => r.km))) : null;
    return { label: vehicleName(v), value: [...percent(v), range].filter(Boolean).join(" · ") || null, detail: plugged[0]?.label ?? null };
  }
  return {
    label: "Auto's",
    value: cars.map((v) => [vehicleName(v), percent(v).join("/")].filter(Boolean).join(" ")).join(" · ") || null,
    detail: plugged.map((p) => `${vehicleName(p.v)}: ${p.label}`).join(" · ") || null,
  };
}

/**
 * The queue to act on first among characters with data, and how many
 * characters failed to update. Characters never fetched have no queue to show.
 */
export function soonestQueue(chars: EveonlineSkillsCharacter[]): { first: EveonlineSkillsCharacter | null; failed: number } {
  const withData = chars.filter((c) => c.fetchedAt);
  return { first: sortQueues(withData)[0] ?? null, failed: chars.filter((c) => c.error).length };
}

/**
 * The next event today or tomorrow: the first that has yet to start, else
 * one under way (an all-day event, a holiday week); with when it is and how
 * many more there are on those days.
 */
export function nextEvent(events: CalendarEvent[], now: string, timezone: string): Summary | null {
  const tomorrow = addDays(localDate(now, timezone), 1);
  const days = agendaDays(events, now, timezone).filter((d) => d.date <= tomorrow);
  const all = days.flatMap((d) => d.events.map((e) => ({ e, day: d.date })));
  const next = all.find(({ e }) => !e.allDay && Date.parse(e.start) >= Date.parse(now)) ?? all[0];
  if (!next) return null;
  const more = all.length - 1;
  return {
    label: "Agenda",
    value: next.e.title || "(geen titel)",
    detail: `${dayLabel(next.day, now, timezone)} ${eventTime(next.e, next.day, timezone)}${more > 0 ? ` · nog ${more}` : ""}`,
  };
}

const WEEK = 7 * 24 * 60 * 60 * 1000;

/**
 * Characters whose queue needs attention within a week: ending by then,
 * empty or paused; most urgent first. Characters never fetched have no queue.
 */
export function skillWarnings(chars: EveonlineSkillsCharacter[], now: string): EveonlineSkillsCharacter[] {
  const end = Date.parse(now) + WEEK;
  return sortQueues(chars.filter((c) => c.fetchedAt && (c.paused || !c.ends || Date.parse(c.ends) < end)));
}

/** A queue's time left as a fraction of a week, for its bar; empty and paused queues have none. */
export function weekFraction(c: EveonlineSkillsCharacter, now: string): number {
  if (c.paused || !c.ends) return 0;
  return Math.min(1, Math.max(0, (Date.parse(c.ends) - Date.parse(now)) / WEEK));
}

export type CarStatus = {
  /** The first level a car reports, battery before tank. */
  level: Level | null;
  charging: boolean;
  pluggedIn: boolean;
  /** "80% om 23:40" while charging, "aangesloten", else the longest range. */
  detail: string | null;
  /** Away faces the car from the house; unknown counts as home. */
  away: boolean;
};

/** What the house scene shows of a car. */
export function carStatus(v: CarVehicle, now: string, timezone: string): CarStatus {
  const c = v.charge;
  const charging = !!c?.charging;
  const pluggedIn = !!c && (c.pluggedIn || c.charging);
  let detail: string | null;
  if (charging) {
    const target = chargeTarget(c!);
    const done = chargeDone(c!, v.updatedAt, now);
    detail = [target != null && `${target}%`, done && `om ${formatTime(done, timezone)}`].filter(Boolean).join(" ") || "laden";
  } else if (pluggedIn) {
    detail = "aangesloten";
  } else {
    detail = v.ranges.length ? formatKm(Math.max(...v.ranges.map((r) => r.km))) : null;
  }
  return { level: levels(v)[0] ?? null, charging, pluggedIn, detail, away: v.atHome === false };
}
