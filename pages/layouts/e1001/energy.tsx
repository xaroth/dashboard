import { formatFlow, formatGas, formatKwh, formatVolt, formatWatt, panelLabel } from "../../lib/energy";
import { formatTime } from "../../lib/format";
import { query, type PageProps, type Result } from "../../lib/page";
import type { P1Usage } from "../../generated/queries";
import styles from "./energy.module.css";

export const data = {
  meter: query("p1.actual"),
  solar: query("solar.production"),
};

type Data = typeof data;

function Updated({ result, label, timezone }: { result: Result<unknown>; label: string; timezone: string }) {
  const { fetchedAt, stale } = result;
  return (
    <span className={stale ? styles.stale : undefined}>
      {label} {stale ? "verouderd, " : ""}
      {fetchedAt ? formatTime(fetchedAt, timezone) : "-"}
    </span>
  );
}

function UsageRow({ name, usage, timezone }: { name: string; usage: P1Usage | null; timezone: string }) {
  // Days count from the first reading stored that day.
  const since = usage ? formatTime(usage.from, timezone) : "00:00";
  return (
    <div className={styles.row}>
      <span className={styles.rowName}>
        {name}
        {since > "00:05" ? <span className={styles.since}>sinds {since}</span> : null}
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
    <div className={styles.screen}>
      <div className={styles.header}>
        <span>Energie</span>
        <span className={styles.updated}>
          <Updated result={data.meter} label="meter" timezone={timezone} />
          <Updated result={data.solar} label="zon" timezone={timezone} />
        </span>
      </div>
      <div className={styles.now}>
        <div className={styles.block}>
          <div className={styles.big}>{m ? formatFlow(m.flowKw) : "-"}</div>
          <div className={styles.label}>
            {!m ? `Geen meterstand${data.meter.error ? `: ${data.meter.error}` : ""}` : Math.round(m.flowKw * 100) < 0 ? "Teruglevering" : "Afname"}
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
        <div className={styles.missing}>Geen panelen</div>
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
    </div>
  );
}
