import { expect, test } from "bun:test";
import {
  chartDomain,
  flowDirection,
  formatFlow,
  formatGas,
  formatKwAxis,
  formatKwh,
  formatVolt,
  formatWatt,
  hourTicks,
  panelLabel,
  startedLate,
} from "./energy";
import { formatTime } from "./format";

const tz = "Europe/Amsterdam";
const minute = 60_000;
const hour = 60 * minute;

test("flow is signed: + taken, - returned", () => {
  expect(formatFlow(1.234)).toBe("+1,23 kW");
  expect(formatFlow(-0.45)).toBe("-0,45 kW");
  expect(formatFlow(-0.004)).toBe("0,00 kW");
});

test("flow direction follows the formatted flow", () => {
  for (const kw of [1.2, 0, -0.004, -0.005, -0.006, -0.45]) {
    expect(flowDirection(kw)).toBe(formatFlow(kw).startsWith("-") ? "Teruglevering" : "Afname");
  }
  expect(flowDirection(-0.006)).toBe("Teruglevering");
  expect(flowDirection(-0.004)).toBe("Afname");
});

test("units with a decimal comma", () => {
  expect(formatWatt(1234.4)).toBe("1.234 W");
  expect(formatWatt(-0.4)).toBe("0 W");
  expect(formatKwh(12.34)).toBe("12,3 kWh");
  expect(formatGas(1.234)).toBe("1,23 m³");
  expect(formatGas(null)).toBe("-");
  expect(formatVolt(230.06)).toBe("230,1 V");
  expect(formatVolt(null)).toBe("-");
});

test("panel labels", () => {
  expect(panelLabel("138292184456", 3)).toBe("456.3");
});

test("axis labels in kW", () => {
  expect(formatKwAxis(1234)).toBe("1,2 kW");
  expect(formatKwAxis(-20)).toBe("0,0 kW");
});

test("a day started late after 00:05 local", () => {
  expect(startedLate("2026-10-24T22:00:00Z", tz)).toBe(false); // 00:00 CEST
  expect(startedLate("2026-10-24T22:05:00Z", tz)).toBe(false);
  expect(startedLate("2026-10-24T22:06:00Z", tz)).toBe(true);
  expect(startedLate("2026-10-25T09:00:00Z", tz)).toBe(true);
  expect(startedLate("2026-10-24T23:00:00Z", "UTC")).toBe(true);
});

const series = (start: string, n: number, step = 15) =>
  Array.from({ length: n }, (_, i) => ({ t: new Date(Date.parse(start) + i * step * minute).toISOString(), v: i as number | null }));

test("chart domain over both series", () => {
  expect(chartDomain([], [])).toBeNull();
  const flow = series("2026-10-25T10:00:00Z", 4).map((p) => ({ ...p, v: p.v! - 2 }));
  const solar = series("2026-10-25T10:00:00Z", 4).map((p) => ({ ...p, v: p.v! * 500 }));
  expect(chartDomain(flow, solar)).toEqual({
    start: Date.parse("2026-10-25T10:00:00Z"),
    end: Date.parse("2026-10-25T11:00:00Z"),
    step: 15 * minute,
    min: -2000 * 1.05,
    max: 1500 * 1.05,
  });
});

test("chart domain spans series that start and end apart", () => {
  const flow = series("2026-10-25T10:00:00Z", 2);
  const solar = series("2026-10-25T09:00:00Z", 3);
  const d = chartDomain(flow, solar)!;
  expect(d.start).toBe(Date.parse("2026-10-25T09:00:00Z"));
  expect(d.end).toBe(Date.parse("2026-10-25T10:30:00Z"));
  expect(d.step).toBe(15 * minute);
  // The longer series no longer decides: solar with more points ends first.
  const d2 = chartDomain(series("2026-10-25T10:00:00Z", 2), series("2026-10-25T08:00:00Z", 3))!;
  expect(d2.start).toBe(Date.parse("2026-10-25T08:00:00Z"));
  expect(d2.end).toBe(Date.parse("2026-10-25T10:30:00Z"));
});

test("chart domain with a single point", () => {
  const d = chartDomain([{ t: "2026-10-25T10:00:00Z", v: null }], [])!;
  expect(d.end - d.start).toBe(15 * minute);
  expect([d.min, d.max]).toEqual([0, 1050]);
});

const labels = (ticks: number[], zone = tz) => ticks.map((t) => formatTime(new Date(t).toISOString(), zone));

test("hour ticks on local hours divisible by 6", () => {
  const start = Date.parse("2026-10-20T09:15:00Z");
  expect(labels(hourTicks(start, start + 24 * hour, tz))).toEqual(["12:00", "18:00", "00:00", "06:00"]);
  // End is exclusive.
  expect(hourTicks(Date.parse("2026-10-20T04:00:00Z"), Date.parse("2026-10-20T10:00:00Z"), tz)).toEqual([Date.parse("2026-10-20T04:00:00Z")]);
});

test("hour ticks across DST", () => {
  // 25 October 2026: 03:00 CEST is 02:00 CET, the day has 25 hours.
  const fallStart = Date.parse("2026-10-24T22:00:00Z");
  const fall = hourTicks(fallStart, fallStart + 25 * hour, tz);
  expect(labels(fall)).toEqual(["00:00", "06:00", "12:00", "18:00"]);
  expect(fall[1]! - fall[0]!).toBe(7 * hour);
  // 29 March 2026: 02:00 CET is 03:00 CEST, the day has 23 hours.
  const springStart = Date.parse("2026-03-28T23:00:00Z");
  const spring = hourTicks(springStart, springStart + 23 * hour, tz);
  expect(labels(spring)).toEqual(["00:00", "06:00", "12:00", "18:00"]);
  expect(spring[1]! - spring[0]!).toBe(5 * hour);
});

test("hour ticks in a zone with a half-hour offset", () => {
  const start = Date.parse("2026-10-20T00:00:00Z");
  expect(labels(hourTicks(start, start + 24 * hour, "Asia/Kolkata"), "Asia/Kolkata")).toEqual(["06:00", "12:00", "18:00", "00:00"]);
});
