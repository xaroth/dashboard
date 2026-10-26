import { CarSeen } from "../components/CarSeen";
import { Missing, PageFrame, Updated } from "../components/PageFrame";
import { chargeLabel, formatKm, keyWarning, levels, openLabel, vehicleName, warningLabels } from "../lib/car";
import { query, type PageProps } from "../lib/page";
import styles from "./cars.module.css";

export const data = {
  cars: query("car.vehicles"),
};

type Data = typeof data;

export default function Cars({ data, now, timezone }: PageProps<Data>) {
  const { value: cars, error } = data.cars;
  if (!cars?.length) {
    return (
      <PageFrame>
        <Missing what="auto's" error={error} />
      </PageFrame>
    );
  }
  // Rows say when each car was seen; the header only marks failing updates.
  const status = data.cars.stale ? <Updated result={data.cars} timezone={timezone} /> : null;
  return (
    <PageFrame title="Auto's" status={status} gap={12}>
      {cars.map((v) => {
        const name = vehicleName(v);
        const range = Math.max(0, ...v.ranges.map((r) => r.km));
        const open = openLabel(v.open);
        const warnings = [keyWarning(v, now), ...warningLabels(v.warnings)].filter((w) => w != null);
        return (
          <div key={v.vin} className={styles.car}>
            <div className={styles.head}>
              <span className={styles.name}>
                {name}
                {name !== v.model ? <span className={styles.model}>{v.model}</span> : null}
              </span>
              <CarSeen stale={false} updatedAt={v.updatedAt} now={now} timezone={timezone} />
            </div>
            <div className={styles.facts}>
              {levels(v).map((l) => (
                <span key={l.label}>
                  {`${l.label} `}
                  <span className={styles.value}>{`${Math.round(l.percent)}%`}</span>
                </span>
              ))}
              {v.ranges.length > 0 ? <span className={styles.value}>{formatKm(range)}</span> : null}
              {v.charge ? <span>{chargeLabel(v.charge, v.updatedAt, timezone)}</span> : null}
              {v.locked != null ? <span className={v.locked ? undefined : styles.value}>{v.locked ? "Op slot" : "Niet op slot"}</span> : null}
              {open ? <span className={styles.value}>{`Open: ${open}`}</span> : null}
              {warnings.length > 0 ? <span className={styles.value}>{`Let op: ${warnings.join(", ")}`}</span> : null}
            </div>
          </div>
        );
      })}
    </PageFrame>
  );
}
