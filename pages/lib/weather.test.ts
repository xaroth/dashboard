import { expect, test } from "bun:test";
import type { WeatherForecast } from "../generated/queries";
import { forecastDay, weatherIcon, weatherLabel, windDirection } from "./weather";

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

test("forecast day by local date", () => {
  const day = (date: string) => ({ date, weatherCode: 0, temperatureMax: 0, temperatureMin: 0, precipitationSum: 0, precipitationProbabilityMax: null, sunrise: date, sunset: date });
  const f = {
    location: { name: "Uithoorn", country: "NL", latitude: 52.24, longitude: 4.83, timezone: "Europe/Amsterdam" },
    daily: [day("2026-10-25T00:00:00+02:00"), day("2026-10-26T00:00:00+01:00")],
  } as unknown as WeatherForecast;
  expect(forecastDay(f, "2026-10-26")?.date).toBe("2026-10-26T00:00:00+01:00");
  expect(forecastDay(f, "2026-10-25")?.date).toBe("2026-10-25T00:00:00+02:00");
  expect(forecastDay(f, "2026-10-27")).toBeUndefined();
  expect(forecastDay(null, "2026-10-26")).toBeUndefined();
});
