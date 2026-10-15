import { expect, test } from "bun:test";
import { formatDateTime, formatDayShort, formatMillimeters, formatTemperature, formatTime } from "./format";

test("Dutch date in the configured timezone", () => {
  expect(formatDateTime("2026-10-16T05:30:00Z", "Europe/Amsterdam")).toBe("vrijdag 16 oktober om 07:30");
});

test("numbers with a decimal comma", () => {
  expect(formatTemperature(16.64)).toBe("16,6°");
  expect(formatTemperature(-0.4, 0)).toBe("-0°");
  expect(formatMillimeters(2.35)).toBe("2,4 mm");
});

test("times and days in the configured timezone", () => {
  expect(formatTime("2026-10-15T22:45:00Z", "Europe/Amsterdam")).toBe("00:45");
  expect(formatDayShort("2026-10-15T22:45:00Z", "Europe/Amsterdam")).toBe("vr 16");
});
