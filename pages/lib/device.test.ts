import { expect, test } from "bun:test";
import { powerLabel, wifiQuality } from "./device";

test("wifi quality from RSSI", () => {
  expect(wifiQuality(-61)).toBe(78);
  expect(wifiQuality(-40)).toBe(100);
  expect(wifiQuality(-105)).toBe(0);
});

test("power: USB, else the battery voltage", () => {
  const wake = { at: "2026-10-16T10:30:00+02:00", usb: false, powerSource: "header" as const, battery: 4.012 };
  expect(powerLabel(wake)).toBe("4,01 V");
  expect(powerLabel({ ...wake, usb: true })).toBe("USB");
  expect(powerLabel({ ...wake, battery: undefined })).toBeNull();
  expect(powerLabel(undefined)).toBeNull();
});
