import { expect, test } from "bun:test";
import { formatFlow, formatGas, formatKwh, formatVolt, formatWatt, panelLabel } from "./energy";

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
  expect(panelLabel("100000000456", 3)).toBe("456.3");
});
