import { formatCount, formatDuration, formatTime } from "../../lib/format";
import { query, type PageProps } from "../../lib/page";
import styles from "./eveonline.module.css";

export const data = {
  status: query("eveonline_status.server"),
};

type Data = typeof data;

export default function EveOnline({ data, now, timezone }: PageProps<Data>) {
  const { value: s, fetchedAt, stale, error } = data.status;
  if (!s) {
    return (
      <div className={styles.screen}>
        <div className={styles.missing}>Geen serverstatus{error ? `: ${error}` : ""}</div>
      </div>
    );
  }
  const facts: [string, string][] = s.startTime
    ? [
        ["Spelers", formatCount(s.players)],
        ["Online sinds", `${formatTime(s.startTime, timezone)} (${formatDuration(Date.parse(now) - Date.parse(s.startTime))})`],
        ["Versie", s.serverVersion],
      ]
    : [];
  return (
    <div className={styles.screen}>
      <div className={styles.header}>
        <span>EVE Online · Tranquility</span>
        <span className={stale ? styles.stale : undefined}>
          {stale ? "Verouderd, " : ""}bijgewerkt {fetchedAt ? formatTime(fetchedAt, timezone) : "-"}
        </span>
      </div>
      <div className={styles.state}>
        {s.online ? "Online" : "Offline"}
        {s.vip ? <span className={styles.vip}>VIP</span> : null}
      </div>
      <div className={styles.facts}>
        {facts.flatMap(([k, v]) => [
          <div key={`${k}-l`} className={styles.label}>{k}</div>,
          <div key={`${k}-v`}>{v}</div>,
        ])}
      </div>
    </div>
  );
}
