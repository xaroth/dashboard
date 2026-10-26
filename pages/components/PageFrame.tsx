import type { ReactNode } from "react";
import { formatTime } from "../lib/format";
import type { Result } from "../lib/page";
import styles from "./PageFrame.module.css";

// In px; they match PageFrame.module.css. The header is one line high unless it wraps.
export const PAD_X = 16;
export const PAD_Y = 10;
export const HEADER_HEIGHT = 28;

export type PageFrameProps = {
  /** Left of the header; without it there is no header. */
  title?: ReactNode;
  /** Right of the header, usually Updated. */
  status?: ReactNode;
  /** Space between the header and each child, in px. */
  gap?: number;
  children: ReactNode;
};

/** A full screen page with a header: a title, and when its data was fetched. */
export function PageFrame({ title, status, gap, children }: PageFrameProps) {
  return (
    <div className={styles.screen} style={{ gap }}>
      {title != null ? (
        <div className={styles.header}>
          <span>{title}</span>
          {status}
        </div>
      ) : null}
      {children}
    </div>
  );
}

export type UpdatedProps = {
  result: Result<unknown>;
  timezone: string;
  /** Names the data when a header shows several: "meter verouderd, 10:58". */
  label?: string;
  /** Prefixes the result's error, for pages that show their data despite it. */
  showError?: boolean;
};

/** When result was fetched, "Verouderd, bijgewerkt 10:58", in bold while stale. */
export function Updated({ result, timezone, label, showError }: UpdatedProps) {
  const { fetchedAt, stale, error } = result;
  const at = fetchedAt ? formatTime(fetchedAt, timezone) : "-";
  return (
    <span className={stale ? styles.stale : undefined}>
      {showError && error ? `Fout: ${error}. ` : ""}
      {label ? `${label} ${stale ? "verouderd, " : ""}${at}` : `${stale ? "Verouderd, " : ""}bijgewerkt ${at}`}
    </span>
  );
}

/** "Geen weerbericht: <error>", centered in the space left. */
export function Missing({ what, error }: { what: string; error?: string | null }) {
  return <div className={styles.missing}>{`Geen ${what}${error ? `: ${error}` : ""}`}</div>;
}
