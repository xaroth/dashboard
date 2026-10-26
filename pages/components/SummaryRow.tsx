import type { ReactNode } from "react";
import { formatTime } from "../lib/format";
import type { Status } from "../lib/home";
import styles from "./SummaryRow.module.css";

export type SummaryRowProps = {
  label: string;
  /** Freshness of what the row shows. */
  status: Status;
  timezone: string;
  /** Without a value the row says what is missing and why. */
  value?: ReactNode;
  detail?: ReactNode;
  /** Missing value text: "weerbericht" shows "Geen weerbericht". */
  what: string;
  /** The value is text, like a headline, or a list: smaller, one line beside the label, two when stacked. */
  prose?: boolean;
  /** Label above the value instead of in a column beside it, for narrow screens. */
  stacked?: boolean;
};

/**
 * One source on a summary page: a label, its key value and a detail line.
 * Stale data shows when it was fetched; missing data shows the error instead.
 */
export function SummaryRow({ label, status, timezone, value, detail, what, prose, stacked }: SummaryRowProps) {
  const missing = value == null || value === "";
  // Short: beside the value there is no room for Updated's sentence.
  const stale =
    !missing && status.stale ? (
      <span className={styles.status}>verouderd{status.fetchedAt ? ` ${formatTime(status.fetchedAt, timezone)}` : ""}</span>
    ) : null;
  const valueClass = prose ? (stacked ? styles.proseStacked : styles.prose) : styles.value;
  return (
    <div className={stacked ? styles.rowStacked : styles.row}>
      <div className={stacked ? styles.headStacked : styles.head}>
        <span className={styles.label}>{label}</span>
        {stacked ? stale : null}
      </div>
      <div className={styles.body}>
        {missing ? <div className={styles.missing}>{`Geen ${what}`}</div> : <div className={valueClass}>{value}</div>}
        {missing && status.error ? (
          <div className={styles.detail}>{status.error}</div>
        ) : detail ? (
          <div className={styles.detail}>{detail}</div>
        ) : null}
      </div>
      {stacked ? null : stale}
    </div>
  );
}
