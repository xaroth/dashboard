import { expect, test } from "bun:test";
import {
  formatCount,
  formatDateTime,
  formatDayShort,
  formatDuration,
  formatMillimeters,
  formatTemperature,
  formatTime,
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
