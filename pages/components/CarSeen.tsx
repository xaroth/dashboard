import { formatSeen } from "../lib/car";
import styles from "./PageFrame.module.css";

export type CarSeenProps = {
  /** Whether the query result is stale, and why. */
  stale: boolean;
  error?: string | null;
  /** When the car last sent data. */
  updatedAt: string | null;
  now: string;
  timezone: string;
};

/**
 * "Gezien 10:25": when the car last sent data. A parked car is polled up to every 12 hours, so old data
 * is normal; only stale data (failing updates) is marked, in bold with its error.
 */
export function CarSeen({ stale, error, updatedAt, now, timezone }: CarSeenProps) {
  const seen = updatedAt ? formatSeen(updatedAt, now, timezone) : "nooit";
  if (!stale) return <span>{`Gezien ${seen}`}</span>;
  return <span className={styles.stale}>{`Verouderd${error ? ` (${error})` : ""}, gezien ${seen}`}</span>;
}
