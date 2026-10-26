import { expect, test } from "bun:test";
import {
  formatAge,
  formatCount,
  formatDateTime,
  formatDayShort,
  formatDayTime,
  formatDuration,
  formatMillimeters,
  formatTemperature,
  formatTime,
  localClock,
} from "./format";

test("Dutch date in the configured timezone", () => {
  expect(formatDateTime("2026-10-16T05:30:00Z", "Europe/Amsterdam")).toBe("vrijdag 16 oktober om 07:30");
});

test("numbers with a decimal comma", () => {
  expect(formatTemperature(16.64)).toBe("16,6°");
  expect(formatTemperature(-0.4, 0)).toBe("0°");
  expect(formatTemperature(-0.04)).toBe("0,0°");
  expect(formatTemperature(-0.6, 0)).toBe("-1°");
  expect(formatMillimeters(2.35)).toBe("2,4 mm");
});

test("times and days in the configured timezone", () => {
  expect(formatTime("2026-10-15T22:45:00Z", "Europe/Amsterdam")).toBe("00:45");
  expect(formatDayShort("2026-10-15T22:45:00Z", "Europe/Amsterdam")).toBe("vr 16");
});

test("counts with a thousands separator", () => {
  expect(formatCount(26017)).toBe("26.017");
});

test("durations in the two largest units", () => {
  expect(formatDuration(-1)).toBe("0 m");
  expect(formatDuration(12 * 60_000 + 59_999)).toBe("12 m");
  expect(formatDuration((5 * 60 + 12) * 60_000)).toBe("5 u 12 m");
  expect(formatDuration((2 * 1440 + 3 * 60 + 40) * 60_000)).toBe("2 d 3 u");
});

test("day and time", () => {
  expect(formatDayTime("2026-10-16T05:30:00Z", "Europe/Amsterdam")).toBe("vr 16 07:30");
});

test("local clock in the configured timezone", () => {
  expect(localClock(Date.parse("2026-10-15T22:45:00Z"), "Europe/Amsterdam")).toEqual({ hour: 0, minute: 45 });
  expect(localClock(Date.parse("2026-10-16T13:05:00Z"), "Asia/Kolkata")).toEqual({ hour: 18, minute: 35 });
});

test("age in its largest unit", () => {
  const now = "2026-10-16T10:30:00+02:00";
  expect(formatAge("2026-10-16T10:18:00+02:00", now)).toBe("12 m");
  expect(formatAge("2026-10-16T07:10:00+02:00", now)).toBe("3 u");
  expect(formatAge("2026-10-14T09:00:00+02:00", now)).toBe("2 d");
  expect(formatAge("2026-10-16T10:31:00+02:00", now)).toBe("0 m");
});
