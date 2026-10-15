import { expect, test } from "bun:test";
import { weatherLabel, windDirection } from "./weather";

test("weather labels", () => {
  expect(weatherLabel(3)).toBe("Bewolkt");
  expect(weatherLabel(42)).toBe("Code 42");
});

test("wind direction", () => {
  for (const [deg, want] of [[0, "N"], [22, "N"], [23, "NO"], [179, "Z"], [270, "W"], [350, "N"], [-90, "W"]] as const) {
    expect(windDirection(deg)).toBe(want);
  }
});
