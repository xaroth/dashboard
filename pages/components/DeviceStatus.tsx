import { battery, wifiQuality, type Battery } from "../lib/device";
import { formatTime } from "../lib/format";
import type { Wake } from "../lib/page";
import styles from "./DeviceStatus.module.css";

// Each arc and battery segment, from low to high, fills from this percentage.
const LEVELS = [1, 34, 67];

const fill = (percent: number | null, at: number) => (percent != null && percent >= at ? "#000" : "#aaa");

const arcs = ["M7.5 10.5a3.5 3.5 0 0 1 5 0", "M4.5 7.5a8 8 0 0 1 11 0", "M1.5 4.5a12.5 12.5 0 0 1 17 0"];

function Wifi({ quality }: { quality: number | null }) {
  return (
    <svg width={20} height={14} viewBox="0 0 20 14">
      {arcs.map((d, i) => (
        <path key={i} d={d} fill="none" stroke={fill(quality, LEVELS[i]!)} strokeWidth={2} strokeLinecap="round" />
      ))}
      <circle cx={10} cy={12.5} r={1.5} fill={fill(quality, LEVELS[0]!)} />
    </svg>
  );
}

function BatteryIcon({ b }: { b: Battery | null }) {
  return (
    <svg width={24} height={13} viewBox="0 0 24 13">
      <rect x={1} y={1} width={19} height={11} rx={2} fill="none" stroke="#000" strokeWidth={1.5} />
      <rect x={21} y={4} width={2} height={5} rx={0.5} fill="#000" />
      {b?.usb ? (
        <path d="M11.5 2.5L7 7h3l-1.5 3.5L13 6h-3z" fill="#000" />
      ) : (
        LEVELS.map((at, i) => <rect key={at} x={3 + i * 5.5} y={3} width={4.5} height={7} fill={b && b.percent >= at ? "#000" : "#fff"} />)
      )}
    </svg>
  );
}

/** The Wi-Fi signal and the battery, and when the screen was drawn under them, small; placed by className. */
export function DeviceStatus({ wake, now, timezone, className }: { wake?: Wake; now: string; timezone: string; className?: string }) {
  const quality = wake?.rssi != null ? wifiQuality(wake.rssi) : null;
  const b = battery(wake);
  return (
    <div className={`${styles.status} ${className ?? ""}`}>
      <div className={styles.line}>
        <div className={styles.pair}>
          <Wifi quality={quality} />
          <span>{quality != null ? `${quality}%` : "-"}</span>
        </div>
        <div className={styles.pair}>
          <BatteryIcon b={b} />
          <span>{b ? (b.usb ? "USB" : `${b.percent}%`) : "-"}</span>
        </div>
      </div>
      <span>Updated: {formatTime(now, timezone)}</span>
    </div>
  );
}
