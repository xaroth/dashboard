import type { Wake } from "./page";
import { formatNumber } from "./format";

/** Wi-Fi signal quality in percent from RSSI in dBm: -100 dBm and below is 0, -50 and above 100. */
export function wifiQuality(rssi: number): number {
  return Math.min(100, Math.max(0, 2 * (rssi + 100)));
}

/** "USB" on USB power, else the battery voltage, "4,01 V"; null when the device did not send it. */
export function powerLabel(wake: Wake | undefined): string | null {
  if (!wake) return null;
  if (wake.usb) return "USB";
  return wake.battery != null ? `${formatNumber(wake.battery, 2)} V` : null;
}
