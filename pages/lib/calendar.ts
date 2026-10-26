import type { CalendarEvent } from "../generated/queries";
import { formatTime } from "./format";

// Intl.DateTimeFormat is slow to construct, so each format is built once per timezone.
const isoDates = new Map<string, Intl.DateTimeFormat>();
const dayNames = new Intl.DateTimeFormat("nl-NL", { weekday: "long", day: "numeric", month: "long", timeZone: "UTC" });
const shortDays = new Intl.DateTimeFormat("nl-NL", { weekday: "short", day: "numeric", month: "short", timeZone: "UTC" });

/** The local date of t as YYYY-MM-DD. */
export function localDate(t: string | number, timezone: string): string {
  let f = isoDates.get(timezone);
  if (!f) isoDates.set(timezone, (f = new Intl.DateTimeFormat("en-CA", { year: "numeric", month: "2-digit", day: "2-digit", timeZone: timezone })));
  return f.format(new Date(t));
}

/** date, YYYY-MM-DD, plus days. */
export function addDays(date: string, days: number): string {
  const d = new Date(`${date}T00:00:00Z`);
  d.setUTCDate(d.getUTCDate() + days);
  return d.toISOString().slice(0, 10);
}

export type AgendaDay = { date: string; events: CalendarEvent[] };

/**
 * Events by local day, in order. An event that started before today, such as
 * a holiday week, is listed under today.
 */
export function agendaDays(events: CalendarEvent[], now: string, timezone: string): AgendaDay[] {
  const today = localDate(now, timezone);
  const days: AgendaDay[] = [];
  for (const e of events) {
    const start = localDate(e.start, timezone);
    const date = start < today ? today : start;
    const last = days.at(-1);
    if (last?.date === date) last.events.push(e);
    else days.push({ date, events: [e] });
  }
  return days;
}

/** At most rows lines, counting each day's heading as one; days left empty are dropped. */
export function fitRows(days: AgendaDay[], rows: number): AgendaDay[] {
  const out: AgendaDay[] = [];
  let left = rows;
  for (const d of days) {
    if (left < 2) break;
    const events = d.events.slice(0, left - 1);
    out.push({ date: d.date, events });
    left -= 1 + events.length;
  }
  return out;
}

/** Vandaag, Morgen, or zaterdag 24 oktober. */
export function dayLabel(date: string, now: string, timezone: string): string {
  const today = localDate(now, timezone);
  if (date === today) return "Vandaag";
  if (date === addDays(today, 1)) return "Morgen";
  return dayNames.format(new Date(`${date}T12:00:00Z`));
}

/**
 * When e happens on day: "Hele dag", "t/m za 1 nov" for all-day events of
 * several days, "09:30 - 09:45", "tot 09:00" for events that started on an
 * earlier day, and "22:00 - ..." for events that end on a later one.
 * day is a local date, YYYY-MM-DD.
 */
export function eventTime(e: CalendarEvent, day: string, timezone: string): string {
  if (e.allDay) {
    // The end is the midnight after the last day.
    const last = addDays(localDate(e.end, timezone), -1);
    return last > day ? `t/m ${shortDate(last)}` : "Hele dag";
  }
  const start = localDate(e.start, timezone);
  const end = localDate(Date.parse(e.end) - 1, timezone);
  if (start < day) return end > day ? "Hele dag" : `tot ${formatTime(e.end, timezone)}`;
  if (e.start === e.end) return formatTime(e.start, timezone);
  return `${formatTime(e.start, timezone)} - ${end > day ? "..." : formatTime(e.end, timezone)}`;
}

/**
 * An event on one day. time is when it starts, "tot 07:00" when it started
 * on an earlier day, null when it fills the day; before and after say it
 * continues from the day before or into the next.
 */
export type DayEvent = { title: string; calendar: string; time: string | null; before: boolean; after: boolean };

/**
 * The events on a local day, YYYY-MM-DD, those filling the day first. Timed
 * events that already ended are left out.
 */
export function dayEvents(events: CalendarEvent[], date: string, now: string, timezone: string): DayEvent[] {
  const allDay: DayEvent[] = [];
  const timed: DayEvent[] = [];
  for (const e of events) {
    const start = Date.parse(e.start);
    const end = Date.parse(e.end);
    const first = localDate(start, timezone);
    // The end is exclusive: an event ending at midnight is not on the next day.
    const last = localDate(Math.max(start, end - 1), timezone);
    if (first > date || last < date) continue;
    const day = { title: e.title || "(geen titel)", calendar: e.calendar, before: first < date, after: last > date };
    if (e.allDay || (first < date && last > date)) allDay.push({ ...day, time: null });
    else if (end >= Date.parse(now)) timed.push({ ...day, time: first < date ? `tot ${formatTime(e.end, timezone)}` : formatTime(e.start, timezone) });
  }
  return [...allDay, ...timed];
}

/** At most rows of items; when some are left out, the last row counts them instead. */
export function fit<T>(items: T[], rows: number): { shown: T[]; more: number } {
  if (items.length <= rows) return { shown: items, more: 0 };
  const shown = items.slice(0, Math.max(0, rows - 1));
  return { shown, more: items.length - shown.length };
}

/** za 1 nov; date is YYYY-MM-DD. */
export function shortDate(date: string): string {
  return shortDays.format(new Date(`${date}T12:00:00Z`));
}

/** Whether a date, YYYY-MM-DD, is a Saturday or Sunday. */
export function isWeekend(date: string): boolean {
  const day = new Date(`${date}T12:00:00Z`).getUTCDay();
  return day === 0 || day === 6;
}
