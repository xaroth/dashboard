import { Missing, PageFrame, Updated } from "../../components/PageFrame";
import { flowDirection, formatFlow, formatGas, formatKwh, formatVolt, formatWatt, panelLabel, startedLate } from "../../lib/energy";
import { formatTime } from "../../lib/format";
import { query, type PageProps } from "../../lib/page";
import type { P1Usage } from "../../generated/queries";
import styles from "./energy.module.css";

export const data = {
  meter: query("p1.actual"),
  solar: query("solar.production"),
};

type Data = typeof data;

function UsageRow({ name, usage, timezone }: { name: string; usage: P1Usage | null; timezone: string }) {
  // Days count from the first reading stored that day.
  return (
    <div className={styles.row}>
      <span className={styles.rowName}>
        {name}
        {usage && startedLate(usage.from, timezone) ? <span className={styles.since}>sinds {formatTime(usage.from, timezone)}</span> : null}
      </span>
      <span className={styles.cell}>{usage ? formatKwh(usage.takenKwh) : "-"}</span>
      <span className={styles.cell}>{usage ? formatKwh(usage.returnedKwh) : "-"}</span>
      <span className={styles.cell}>{usage ? formatGas(usage.gasM3) : "-"}</span>
    </div>
  );
}

export default function Energy({ data, viewport, timezone }: PageProps<Data>) {
  const m = data.meter.value;
  const s = data.solar.value;
  const panels = s?.panels ?? [];
  // At least 100 W, so a few watts at dusk do not fill the bars.
  const maxPanel = Math.max(100, ...panels.map((p) => p.powerW));
  return (
    <PageFrame
      title="Energie"
      gap={14}
      status={
        <span className={styles.updated}>
          <Updated result={data.meter} label="meter" timezone={timezone} />
          <Updated result={data.solar} label="zon" timezone={timezone} />
        </span>
      }
    >
      <div className={styles.now}>
        <div className={styles.block}>
          <div className={styles.big}>{m ? formatFlow(m.flowKw) : "-"}</div>
          <div className={styles.label}>
            {!m ? `Geen meterstand${data.meter.error ? `: ${data.meter.error}` : ""}` : flowDirection(m.flowKw)}
          </div>
        </div>
        <div className={styles.block}>
          <div className={styles.big}>{s ? formatWatt(s.powerW) : "-"}</div>
          <div className={styles.label}>{s ? "Zon" : `Geen zon${data.solar.error ? `: ${data.solar.error}` : ""}`}</div>
        </div>
        <div className={styles.phases}>
          {(["l1", "l2", "l3"] as const).map((l) => (
            <span key={l}>
              {l.toUpperCase()} {formatVolt(m?.voltage[l] ?? null)}
            </span>
          ))}
        </div>
      </div>
      <div className={styles.usage}>
        <div className={`${styles.row} ${styles.rowHead}`}>
          <span className={styles.rowName} />
          <span className={styles.cell}>Afname</span>
          <span className={styles.cell}>Terug</span>
          <span className={styles.cell}>Gas</span>
        </div>
        <UsageRow name="Vandaag" usage={m?.today ?? null} timezone={timezone} />
        <UsageRow name="Gisteren" usage={m?.yesterday ?? null} timezone={timezone} />
      </div>
      {panels.length === 0 ? (
        <Missing what="panelen" />
      ) : (
        <div
          className={styles.panels}
          // Two rows in portrait: 17 panels do not fit across 480 px.
          style={{ gridTemplateColumns: `repeat(${viewport.portrait ? Math.ceil(panels.length / 2) : panels.length}, 1fr)` }}
        >
          {panels.map((p) => (
            <div key={`${p.inverter}:${p.port}`} className={styles.panel}>
              <div className={styles.barTrack}>
                <span className={styles.panelValue}>{Math.round(p.powerW)}</span>
                {/* 85% leaves room for the value above the bar. */}
                <div className={styles.bar} style={{ height: `${Math.round((85 * Math.max(0, p.powerW)) / maxPanel)}%` }} />
              </div>
              <span className={styles.panelLabel}>{panelLabel(p.inverter, p.port)}</span>
            </div>
          ))}
        </div>
      )}
    </PageFrame>
  );
}
