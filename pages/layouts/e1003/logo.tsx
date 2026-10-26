import styles from "./logo.module.css";

export const data = {};

// Sent at setup; the firmware keeps it as its boot screen and draws its
// messages over the bottom part, so the mark stays above that.
export default function Logo() {
  return (
    <div className={styles.screen}>
      <div className={styles.mark}>Dashboard</div>
    </div>
  );
}
