import { expect, test } from "bun:test";
import type { CalendarEvent } from "../generated/queries";
import { agendaDays, dayLabel, eventTime, fitRows, localDate } from "./calendar";

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
