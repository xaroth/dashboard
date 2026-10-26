import type { Wake } from "./page";

/** Wi-Fi signal quality in percent from RSSI in dBm: -100 dBm and below is 0, -50 and above 100. */
export function wifiQuality(rssi: number): number {
  return Math.min(100, Math.max(0, 2 * (rssi + 100)));
}

// Resting voltage to charge of a generic single-cell lithium battery, highest first.
// Not measured on the reTerminals; replace with a discharge curve of the device.
const CURVE: [volts: number, percent: number][] = [
  [4.2, 100],
  [4.1, 90],
  [4.0, 80],
  [3.92, 70],
  [3.85, 60],
  [3.8, 50],
  [3.75, 40],
  [3.7, 30],
  [3.65, 20],
  [3.5, 5],
  [3.3, 0],
];

/** Battery charge in percent from its resting voltage, interpolated along CURVE. */
export function batteryPercent(volts: number): number {
  if (volts >= CURVE[0]![0]) return 100;
  for (let i = 1; i < CURVE.length; i++) {
    const [v1, p1] = CURVE[i]!;
    if (volts >= v1) {
      const [v0, p0] = CURVE[i - 1]!;
      return Math.round(p1 + ((volts - v1) * (p0 - p1)) / (v0 - v1));
    }
  }
  return 0;
}

export type Battery = { usb: true } | { usb: false; percent: number };

/** On USB power, the battery's charge, or null when the device did not send its voltage. */
export function battery(wake: Wake | undefined): Battery | null {
  if (!wake) return null;
  if (wake.usb) return { usb: true };
  return wake.battery != null ? { usb: false, percent: batteryPercent(wake.battery) } : null;
}
