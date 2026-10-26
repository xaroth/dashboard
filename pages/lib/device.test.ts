import { expect, test } from "bun:test";
import { battery, batteryPercent, wifiQuality } from "./device";

test("wifi quality from RSSI", () => {
  expect(wifiQuality(-61)).toBe(78);
  expect(wifiQuality(-40)).toBe(100);
  expect(wifiQuality(-105)).toBe(0);
});

test("battery percent along the curve", () => {
  expect(batteryPercent(4.25)).toBe(100);
  expect(batteryPercent(4.2)).toBe(100);
  expect(batteryPercent(4.01)).toBe(81);
  expect(batteryPercent(3.775)).toBe(45);
  expect(batteryPercent(3.4)).toBe(3);
  expect(batteryPercent(3.1)).toBe(0);
});

test("battery: USB, else the charge from the voltage", () => {
  const wake = { at: "2026-10-16T10:30:00+02:00", usb: false, powerSource: "header" as const, battery: 4.01 };
  expect(battery(wake)).toEqual({ usb: false, percent: 81 });
  expect(battery({ ...wake, usb: true })).toEqual({ usb: true });
  expect(battery({ ...wake, battery: undefined })).toBeNull();
  expect(battery(undefined)).toBeNull();
});
