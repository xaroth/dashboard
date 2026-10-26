import { expect, test } from "bun:test";
import type { CalendarEvent } from "../generated/queries";
import { agendaDays, dayEvents, dayLabel, eventTime, fit, fitRows, isWeekend, localDate, shortDate } from "./calendar";

const tz = "Europe/Amsterdam";
const now = "2026-10-22T08:00:00+02:00";

const event = (title: string, start: string, end: string, allDay = false): CalendarEvent => ({
  calendar: "family",
  title,
  start,
  end,
  allDay,
  location: "",
});

test("local dates in the configured timezone", () => {
  expect(localDate("2026-10-21T22:30:00Z", tz)).toBe("2026-10-22");
  expect(localDate("2026-10-21T22:30:00Z", "UTC")).toBe("2026-10-21");
});

test("events by day, ongoing ones under today", () => {
  const days = agendaDays(
    [
      event("Vakantie", "2026-10-17T00:00:00+02:00", "2026-10-24T00:00:00+02:00", true),
      event("Standup", "2026-10-22T09:30:00+02:00", "2026-10-22T09:45:00+02:00"),
      event("Tandarts", "2026-10-23T08:30:00+02:00", "2026-10-23T09:15:00+02:00"),
    ],
    now,
    tz,
  );
  expect(days.map((d) => [d.date, d.events.map((e) => e.title)])).toEqual([
    ["2026-10-22", ["Vakantie", "Standup"]],
    ["2026-10-23", ["Tandarts"]],
  ]);
});

test("rows include day headings", () => {
  const e = event("x", now, now);
  const days = [
    { date: "2026-10-22", events: [e, e] },
    { date: "2026-10-23", events: [e, e] },
  ];
  expect(fitRows(days, 4).map((d) => d.events.length)).toEqual([2]);
  expect(fitRows(days, 5).map((d) => d.events.length)).toEqual([2, 1]);
  expect(fitRows(days, 1)).toEqual([]);
});

test("day labels", () => {
  expect(dayLabel("2026-10-22", now, tz)).toBe("Vandaag");
  expect(dayLabel("2026-10-23", now, tz)).toBe("Morgen");
  expect(dayLabel("2026-10-24", now, tz)).toBe("zaterdag 24 oktober");
});

test("event times", () => {
  const day = "2026-10-22";
  expect(eventTime(event("a", "2026-10-22T00:00:00+02:00", "2026-10-23T00:00:00+02:00", true), day, tz)).toBe("Hele dag");
  expect(eventTime(event("a", "2026-10-17T00:00:00+02:00", "2026-11-02T00:00:00+01:00", true), day, tz)).toBe("t/m zo 1 nov");
  expect(eventTime(event("a", "2026-10-22T09:30:00+02:00", "2026-10-22T09:45:00+02:00"), day, tz)).toBe("09:30 - 09:45");
  expect(eventTime(event("a", "2026-10-22T09:30:00+02:00", "2026-10-22T09:30:00+02:00"), day, tz)).toBe("09:30");
  expect(eventTime(event("a", "2026-10-21T22:00:00Z", "2026-10-22T07:00:00Z"), day, tz)).toBe("00:00 - 09:00");
  expect(eventTime(event("a", "2026-10-21T20:00:00+02:00", "2026-10-22T09:00:00+02:00"), day, tz)).toBe("tot 09:00");
  expect(eventTime(event("a", "2026-10-21T20:00:00+02:00", "2026-10-23T09:00:00+02:00"), day, tz)).toBe("Hele dag");
  expect(eventTime(event("a", "2026-10-22T22:00:00+02:00", "2026-10-23T06:00:00+02:00"), day, tz)).toBe("22:00 - ...");
  expect(eventTime(event("a", "2026-10-22T22:00:00+02:00", "2026-10-23T00:00:00+02:00"), day, tz)).toBe("22:00 - 00:00");
});

test("events on a day: filling the day first, ended ones left out", () => {
  const at = "2026-10-16T10:30:00+02:00";
  const holiday = event("Vakantie", "2026-10-10T00:00:00+02:00", "2026-10-19T00:00:00+02:00", true);
  const standup = { ...event("Standup", "2026-10-16T09:30:00+02:00", "2026-10-16T09:45:00+02:00"), calendar: "work" };
  const dentist = event("Tandarts", "2026-10-16T14:00:00+02:00", "2026-10-16T14:45:00+02:00");
  const night = event("Nachtdienst", "2026-10-16T22:00:00+02:00", "2026-10-17T07:00:00+02:00");
  const trip = event("Reis", "2026-10-15T08:00:00+02:00", "2026-10-18T20:00:00+02:00");
  const all = [holiday, standup, dentist, night, trip];
  const e = (title: string, time: string | null, before: boolean, after: boolean) => ({ title, calendar: "family", time, before, after });
  expect(dayEvents(all, "2026-10-16", at, tz)).toEqual([
    e("Vakantie", null, true, true),
    e("Reis", null, true, true),
    e("Tandarts", "14:00", false, false),
    e("Nachtdienst", "22:00", false, true),
  ]);
  expect(dayEvents(all, "2026-10-17", at, tz)).toEqual([e("Vakantie", null, true, true), e("Reis", null, true, true), e("Nachtdienst", "tot 07:00", true, false)]);
  expect(dayEvents(all, "2026-10-18", at, tz)).toEqual([e("Vakantie", null, true, false), e("Reis", "tot 20:00", true, false)]);
  expect(dayEvents([{ ...dentist, title: "" }], "2026-10-16", at, tz)).toEqual([e("(geen titel)", "14:00", false, false)]);
  expect(dayEvents([{ ...standup, start: "2026-10-16T11:00:00+02:00", end: "2026-10-16T11:15:00+02:00" }], "2026-10-16", at, tz)[0]?.calendar).toBe("work");
  expect(dayEvents([holiday], "2026-10-19", at, tz)).toEqual([]);
});

test("fit: the last row counts what is left out", () => {
  expect(fit([1, 2, 3], 3)).toEqual({ shown: [1, 2, 3], more: 0 });
  expect(fit([1, 2, 3, 4], 3)).toEqual({ shown: [1, 2], more: 2 });
});

test("short dates and weekends", () => {
  expect(shortDate("2026-11-01")).toBe("zo 1 nov");
  expect(isWeekend("2026-10-31")).toBe(true);
  expect(isWeekend("2026-11-01")).toBe(true);
  expect(isWeekend("2026-11-02")).toBe(false);
});
