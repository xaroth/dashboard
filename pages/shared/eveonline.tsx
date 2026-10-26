import { Missing, PageFrame, Updated } from "../components/PageFrame";
import { formatCount, formatDuration, formatTime } from "../lib/format";
import { query, type PageProps } from "../lib/page";
import styles from "./eveonline.module.css";

export const data = {
  status: query("eveonline_status.server"),
};

type Data = typeof data;

export default function EveOnline({ data, now, timezone }: PageProps<Data>) {
  const { value: s, error } = data.status;
  if (!s) {
    return (
      <PageFrame>
        <Missing what="serverstatus" error={error} />
      </PageFrame>
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
    <PageFrame title="EVE Online · Tranquility" status={<Updated result={data.status} timezone={timezone} />} gap={24}>
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
    </PageFrame>
  );
}
