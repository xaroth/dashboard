import styles from "./StaleMark.module.css";

/** A black "!" in a circle, placed by className: the data beside it is stale or missing. */
export function StaleMark({ className }: { className?: string }) {
  return <div className={`${styles.stale} ${className ?? ""}`}>!</div>;
}
