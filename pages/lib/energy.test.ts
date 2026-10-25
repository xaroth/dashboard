import { expect, test } from "bun:test";
import { formatFlow, formatGas, formatKwAxis, formatKwh, formatVolt, formatWatt, panelLabel, runs } from "./energy";

test("flow is signed: + taken, - returned", () => {
  expect(formatFlow(1.234)).toBe("+1,23 kW");
  expect(formatFlow(-0.45)).toBe("-0,45 kW");
  expect(formatFlow(-0.004)).toBe("0,00 kW");
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

test("runs break at gaps", () => {
  const p = (m: number, v: number | null) => ({ t: `2026-10-25T12:0${m}:00Z`, v });
  const got = runs([p(0, null), p(1, 1), p(2, 2), p(3, null), p(4, 4)]);
  expect(got.map((r) => r.map((x) => x.v))).toEqual([[1, 2], [4]]);
  expect(got[0]![0]!.t).toBe(Date.parse("2026-10-25T12:01:00Z"));
  expect(runs([])).toEqual([]);
});

test("axis labels in kW", () => {
  expect(formatKwAxis(1234)).toBe("1,2 kW");
  expect(formatKwAxis(-20)).toBe("0,0 kW");
});
