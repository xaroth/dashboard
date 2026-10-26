import { powerLabel, wifiQuality } from "../lib/device";
import { formatTime } from "../lib/format";
import type { Wake } from "../lib/page";
import styles from "./DeviceStatus.module.css";

// Arcs from the inside out; each lights up at this quality, in percent.
const arcs = [
  { d: "M7.5 10.5a3.5 3.5 0 0 1 5 0", at: 1 },
  { d: "M4.5 7.5a8 8 0 0 1 11 0", at: 34 },
  { d: "M1.5 4.5a12.5 12.5 0 0 1 17 0", at: 67 },
];

function Wifi({ quality }: { quality: number | null }) {
  return (
    <svg width={20} height={14} viewBox="0 0 20 14">
      {arcs.map((a) => (
        <path key={a.at} d={a.d} fill="none" stroke={quality != null && quality >= a.at ? "#000" : "#aaa"} strokeWidth={2} strokeLinecap="round" />
      ))}
      <circle cx={10} cy={12.5} r={1.5} fill={quality ? "#000" : "#aaa"} />
    </svg>
  );
}

/** When the screen was drawn, the Wi-Fi signal and the battery, small; placed by className. */
export function DeviceStatus({ wake, now, timezone, className }: { wake?: Wake; now: string; timezone: string; className?: string }) {
  const quality = wake?.rssi != null ? wifiQuality(wake.rssi) : null;
  const power = powerLabel(wake);
  return (
    <div className={`${styles.status} ${className ?? ""}`}>
      <div className={styles.line}>
        {quality != null ? <span>{quality}%</span> : null}
        <Wifi quality={quality} />
        <span>Upd: {formatTime(now, timezone)}</span>
      </div>
      {power ? <span>{power}</span> : null}
    </div>
  );
}
