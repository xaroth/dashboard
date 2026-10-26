import type { CalendarEvent, CarVehicle, EveonlineSkillsCharacter } from "../generated/queries";
import { addDays, agendaDays, dayLabel, eventTime, localDate } from "./calendar";
import { chargeLabel, formatKm, levels, vehicleName } from "./car";
import { sortQueues } from "./eveonline";
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
