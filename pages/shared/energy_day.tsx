import { EnergyChart } from "../components/EnergyChart";
import { Missing, PAD_X, PAD_Y, PageFrame, Updated } from "../components/PageFrame";
import { linear } from "../lib/chart";
import { chartDomain, formatKwAxis, hourTicks } from "../lib/energy";
import { formatTime } from "../lib/format";
import { query, type PageProps } from "../lib/page";
import styles from "./energy_day.module.css";

export const data = {
  flow: query("p1.flow", { hours: 24, stepMinutes: 15 }),
  solar: query("solar.power", { hours: 24, stepMinutes: 15 }),
};

type Data = typeof data;

// Axis width in px; it matches energy_day.module.css.
const AXIS = 70;

export default function EnergyDay({ data, viewport, timezone }: PageProps<Data>) {
  const flow = data.flow.value ?? [];
  const solar = data.solar.value ?? [];
  const domain = chartDomain(flow, solar);
  if (!domain) {
    return (
      <PageFrame>
        <Missing what="energiegegevens" error={data.flow.error ?? data.solar.error} />
      </PageFrame>
    );
  }
  const { start, end, min, max } = domain;
  const width = viewport.width - 2 * PAD_X - AXIS;
  const height = Math.max(120, viewport.height - 2 * PAD_Y - 120);
  const x = linear(start, end, 0, width);
  // One status for both: the flow's, unless only solar was fetched.
  const updated = { ...data.flow, stale: data.flow.stale || data.solar.stale, fetchedAt: data.flow.fetchedAt ?? data.solar.fetchedAt };
  return (
    <PageFrame title="Energie, laatste 24 uur" status={<Updated result={updated} timezone={timezone} />} gap={12}>
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
        <EnergyChart solar={solar} flow={flow} domain={domain} width={width} height={height} />
      </div>
      <div className={styles.hours} style={{ width, marginLeft: AXIS }}>
        {hourTicks(start, end, timezone).map((t) => (
          <span key={t} className={styles.hourLabel} style={{ left: x(t) - 25 }}>
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
    </PageFrame>
  );
}
