import { formatDateTime } from "../../lib/format";
import type { PageProps } from "../../lib/page";
import styles from "./hello.module.css";

export const data = {};

// Skeleton page: shows what the service knows about the device that asked.
export default function Hello({ device, viewport, now, timezone }: PageProps<typeof data>) {
  const facts: [string, string][] = [
    ["Device", `${device.name} (${device.model})`],
    ["Viewport", `${viewport.width}x${viewport.height}, rotation ${device.rotation}`],
    ["Wake", device.wake?.source ?? "-"],
    ["Battery", device.wake?.battery != null ? `${device.wake.battery.toFixed(2)} V` : "-"],
    ["RSSI", device.wake?.rssi != null ? `${device.wake.rssi} dBm` : "-"],
    ["Firmware", device.wake?.firmware ?? "-"],
  ];
  return (
    <div className={styles.screen}>
      <div className={styles.title}>Dashboard</div>
      <div className={styles.time}>{formatDateTime(now, timezone)}</div>
      <div className={styles.facts}>
        {facts.flatMap(([k, v]) => [
          <div key={`${k}-l`} className={styles.label}>{k}</div>,
          <div key={`${k}-v`}>{v}</div>,
        ])}
      </div>
      <div className={styles.swatches}>
        {["#000", "#555", "#aaa", "#fff"].map((c) => (
          <div key={c} className={styles.swatch} style={{ background: c }} />
        ))}
      </div>
    </div>
  );
}
