import { EnergyChart } from "../../components/EnergyChart";
import { formatKwAxis } from "../../lib/energy";
import { formatTime } from "../../lib/format";
import { query, type PageProps } from "../../lib/page";
import styles from "./energy_day.module.css";

export const data = {
  flow: query("p1.flow", { hours: 24, stepMinutes: 15 }),
  solar: query("solar.power", { hours: 24, stepMinutes: 15 }),
};

type Data = typeof data;

const hour = 3_600_000;

export default function EnergyDay({ data, viewport, timezone }: PageProps<Data>) {
  const flow = data.flow.value ?? [];
  const solar = data.solar.value ?? [];
  const first = (flow.length >= solar.length ? flow : solar)[0];
  if (!first) {
    const error = data.flow.error ?? data.solar.error;
    return (
      <div className={styles.screen}>
        <div className={styles.missing}>Geen energiegegevens{error ? `: ${error}` : ""}</div>
      </div>
    );
  }
  const points = flow.length >= solar.length ? flow : solar;
  const start = Date.parse(first.t);
  const step = points[1] ? Date.parse(points[1].t) - start : 15 * 60_000;
  const end = Date.parse(points[points.length - 1]!.t) + step;
  const watts = [
    ...solar.flatMap((p) => (p.v == null ? [] : [p.v])),
    ...flow.flatMap((p) => (p.v == null ? [] : [p.v * 1000])),
  ];
  // At least 1 kW up and the zero line in view.
  const max = Math.max(1000, ...watts) * 1.05;
  const min = Math.min(0, ...watts) * 1.05;
  const width = viewport.width - 64 - 70;
  const height = Math.max(120, viewport.height - 160);
  // Labels on the local hours divisible by 6.
  const labels: number[] = [];
  for (let t = Math.ceil(start / hour) * hour; t < end; t += hour) {
    if (Number(formatTime(new Date(t).toISOString(), timezone).slice(0, 2)) % 6 === 0) labels.push(t);
  }
  const stale = data.flow.stale || data.solar.stale;
  const fetchedAt = data.flow.fetchedAt ?? data.solar.fetchedAt;
  return (
    <div className={styles.screen}>
      <div className={styles.header}>
        <span>Energie, laatste 24 uur</span>
        <span className={stale ? styles.stale : undefined}>
          {stale ? "Verouderd, " : ""}bijgewerkt {fetchedAt ? formatTime(fetchedAt, timezone) : "-"}
        </span>
      </div>
      <div className={styles.chartRow}>
        <div className={styles.axis} style={{ height }}>
          <span className={styles.axisLabel} style={{ top: 0 }}>
            {formatKwAxis(max)}
          </span>
          {/* The zero line, unless it is the bottom edge. */}
          {min < 0 ? (
            <span className={styles.axisLabel} style={{ top: (max / (max - min)) * height - 10 }}>
              0
            </span>
          ) : null}
          <span className={styles.axisLabel} style={{ bottom: 0 }}>
            {min < 0 ? formatKwAxis(min) : "0"}
          </span>
        </div>
        <EnergyChart solar={solar} flow={flow} start={start} end={end} step={step} min={min} max={max} width={width} height={height} />
      </div>
      <div className={styles.hours} style={{ width, marginLeft: 70 }}>
        {labels.map((t) => (
          <span key={t} className={styles.hourLabel} style={{ left: ((t - start) / (end - start)) * width - 25 }}>
            {formatTime(new Date(t).toISOString(), timezone)}
          </span>
        ))}
      </div>
      <div className={styles.legend}>
        <span className={styles.legendItem}>
          <span className={styles.swatchSolar} /> Zon
        </span>
        <span className={styles.legendItem}>
          <span className={styles.swatchFlow} /> Net: + afname, - teruglevering
        </span>
      </div>
    </div>
  );
}
