import { CarSeen } from "../components/CarSeen";
import { Missing, PageFrame } from "../components/PageFrame";
import { chargeLabel, formatKm, keyWarning, levels, openLabel, rangeLabel, vehicleName, warningLabels } from "../lib/car";
import { formatTemperature } from "../lib/format";
import { query, type PageProps } from "../lib/page";
import styles from "./car.module.css";

export const data = {
  // id is a short name or VIN, set per device as car.car; without it, the first car.
  car: query("car.vehicle"),
};

type Data = typeof data;

export default function Car({ data, viewport, now, timezone }: PageProps<Data>) {
  const { value: v, error } = data.car;
  if (!v) {
    return (
      <PageFrame>
        <Missing what="auto" error={error} />
      </PageFrame>
    );
  }
  const name = vehicleName(v);
  // The longest range first: a plug-in hybrid's total when it reports one.
  const ranges = [...v.ranges].sort((a, b) => b.km - a.km);
  const open = openLabel(v.open);
  const warnings = [keyWarning(v, now), ...warningLabels(v.warnings)].filter((w) => w != null);
  const lv = levels(v);
  // A plug-in hybrid's battery, tank and range only fit across 800 px smaller.
  const big = { fontSize: !viewport.portrait && lv.length + Math.min(1, ranges.length) > 2 ? 72 : undefined };
  type Fact = { label: string; value: string; alert?: boolean; wide?: boolean };
  const facts: Fact[] = [];
  if (ranges.length > 1) {
    // The total is already the big number.
    const parts = ranges.filter((r) => r.type.toUpperCase() !== "PHEV");
    facts.push({ label: "Bereik", value: parts.map((r) => `${rangeLabel(r.type)} ${formatKm(r.km)}`).join(", "), wide: true });
  }
  if (v.locked != null) facts.push({ label: "Slot", value: v.locked ? "Op slot" : "Niet op slot", alert: !v.locked });
  if (open) facts.push({ label: "Open", value: open, alert: true });
  if (v.ignitionOn) facts.push({ label: "Contact", value: "Aan" });
  if (v.climateOn) facts.push({ label: "Klimaat", value: "Aan" });
  if (v.outsideTemperature != null) facts.push({ label: "Buiten", value: formatTemperature(v.outsideTemperature) });
  if (v.odometerKm != null) facts.push({ label: "Km-stand", value: formatKm(v.odometerKm) });
  return (
    <PageFrame
      title={name === v.model ? name : `${name}, ${v.model}`}
      status={<CarSeen stale={data.car.stale} error={error} updatedAt={v.updatedAt} now={now} timezone={timezone} />}
      gap={16}
    >
      <div className={styles.top}>
        {lv.map((l) => (
          <div key={l.label} className={styles.block}>
            <div className={styles.big} style={big}>{`${Math.round(l.percent)}%`}</div>
            <div className={styles.track}>
              <div className={styles.fill} style={{ width: `${Math.max(0, Math.min(100, l.percent))}%` }} />
            </div>
            <div className={styles.label}>{l.label}</div>
          </div>
        ))}
        {ranges.length > 0 ? (
          <div className={styles.block}>
            <div className={styles.big} style={big}>{formatKm(ranges[0]!.km)}</div>
            <div className={styles.label}>Bereik</div>
          </div>
        ) : null}
      </div>
      {v.charge ? <div className={styles.charge}>{chargeLabel(v.charge, v.updatedAt, now, timezone)}</div> : null}
      <div className={styles.facts}>
        {facts.map((f) => (
          <div key={f.label} className={styles.fact} style={{ width: viewport.portrait || f.wide ? "100%" : "50%" }}>
            <span className={styles.factLabel}>{f.label}</span>
            <span className={f.alert ? styles.alert : undefined}>{f.value}</span>
          </div>
        ))}
      </div>
      {warnings.length > 0 ? <div className={styles.warnings}>{`Let op: ${warnings.join(", ")}`}</div> : null}
    </PageFrame>
  );
}
