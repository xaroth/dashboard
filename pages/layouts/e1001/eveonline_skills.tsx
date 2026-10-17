import { SkillQueues } from "../../components/SkillQueues";
import { formatTime } from "../../lib/format";
import { query, type PageProps } from "../../lib/page";
import styles from "./eveonline_skills.module.css";

export const data = {
  queues: query("eveonline_skills.queue", { skills: 20 }),
};

type Data = typeof data;

// Padding and header height in px; they match eveonline_skills.module.css.
const PAD_X = 32;
const PAD_Y = 20;
const HEADER = 28 + 20;

export default function EveOnlineSkills({ data, viewport, now, timezone }: PageProps<Data>) {
  const { value: queues, fetchedAt, stale, error } = data.queues;
  return (
    <div className={styles.screen}>
      <div className={styles.header}>
        <span>EVE Online · Skillqueues</span>
        <span className={stale ? styles.stale : undefined}>
          {error ? `Fout: ${error}. ` : ""}
          {stale ? "Verouderd, " : ""}bijgewerkt {fetchedAt ? formatTime(fetchedAt, timezone) : "-"}
        </span>
      </div>
      {!queues && error ? (
        <div className={styles.missing}>Geen skillqueues</div>
      ) : (
        <SkillQueues
          characters={queues ?? []}
          now={now}
          timezone={timezone}
          width={viewport.width - 2 * PAD_X}
          height={viewport.height - 2 * PAD_Y - HEADER}
        />
      )}
    </div>
  );
}
