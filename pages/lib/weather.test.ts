import { expect, test } from "bun:test";
import { weatherIcon, weatherLabel, windDirection } from "./weather";

test("weather labels", () => {
  expect(weatherLabel(3)).toBe("Bewolkt");
  expect(weatherLabel(42)).toBe("Code 42");
});

test("wind direction", () => {
  for (const [deg, want] of [[0, "N"], [22, "N"], [23, "NO"], [179, "Z"], [270, "W"], [350, "N"], [-90, "W"]] as const) {
    expect(windDirection(deg)).toBe(want);
  }
});

test("weather icons", () => {
  for (const [code, want] of [[0, "sun"], [2, "partly"], [3, "cloud"], [45, "fog"], [53, "rain"], [66, "rain"], [81, "rain"], [73, "snow"], [86, "snow"], [96, "thunder"], [42, "cloud"]] as const) {
    expect(weatherIcon(code)).toBe(want);
  }
  expect(weatherIcon(1, false)).toBe("moon");
  expect(weatherIcon(2, false)).toBe("partlyNight");
  expect(weatherIcon(61, false)).toBe("rain");
});
