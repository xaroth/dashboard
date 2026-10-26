import styles from "./StaleMark.module.css";

/**
 * A black "!" in a circle: the data beside it is stale or missing. Placed
 * absolutely by className, or inline in a line of text.
 */
export function StaleMark({ className, inline }: { className?: string; inline?: boolean }) {
  return <div className={`${inline ? styles.inline : styles.stale} ${className ?? ""}`}>!</div>;
}
