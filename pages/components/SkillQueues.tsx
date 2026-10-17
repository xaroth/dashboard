import type { EveonlineSkillsCharacter } from "../generated/queries";
import { skillLevel, sortQueues } from "../lib/eveonline";
import { formatCount, formatDayTime, formatDuration } from "../lib/format";
import styles from "./SkillQueues.module.css";

// Row heights in px; they match SkillQueues.module.css.
const HEAD = 36;
const SKILL = 28;
const GAP = 16;
const COLUMN_WIDTH = 560;
const COLUMN_GAP = 32;
// Below this a column shows how long a queue lasts, not when it ends.
const NARROW = 520;

export type SkillQueuesProps = {
  characters: EveonlineSkillsCharacter[];
  now: string;
  timezone: string;
  width: number;
  height: number;
};

/**
 * Every character's skill queue, most urgent first. With room to spare each
 * character lists its next skills; otherwise only when its queue runs out.
 */
export function SkillQueues({ characters, now, timezone, width, height }: SkillQueuesProps) {
  if (characters.length === 0) return <div className={styles.none}>Geen characters met skillqueue</div>;
  // More columns than the width suggests when the rows would not fit otherwise.
  const fit = Math.max(1, Math.floor((height + GAP) / (HEAD + GAP)));
  const columns = Math.min(characters.length, Math.max(1, Math.floor(width / COLUMN_WIDTH), Math.ceil(characters.length / fit)));
  const short = (width - (columns - 1) * COLUMN_GAP) / columns < NARROW;
  const rows = Math.ceil(characters.length / columns);
  const perRow = (height + GAP) / rows - GAP;
  const lines = Math.max(0, Math.floor((perRow - HEAD) / SKILL));
  // Skill points take a line once there is room for a skill as well.
  const showSp = lines >= 2;
  const skills = showSp ? lines - 1 : lines;
  const sorted = sortQueues(characters);
  return (
    <div className={styles.grid} style={{ width, height, gridTemplateColumns: `repeat(${columns}, 1fr)` }}>
      {sorted.map((c) => (
        <div key={c.id} className={styles.character}>
          <div className={styles.head}>
            <span className={styles.name}>{c.name}</span>
            <Ends c={c} now={now} timezone={timezone} short={short} />
          </div>
          {showSp ? (
            <div className={`${styles.skill} ${styles.sp}`}>
              {formatCount(c.totalSp)} SP{c.unallocatedSp > 0 ? `, ${formatCount(c.unallocatedSp)} niet toegewezen` : ""}
            </div>
          ) : null}
          {c.skills.slice(0, skills).map((s, i) => (
            <div key={i} className={styles.skill}>
              <span className={styles.skillName}>
                {s.name || `Skill ${s.id}`} {skillLevel(s.level)}
              </span>
              <span>{s.finish ? formatDayTime(s.finish, timezone) : ""}</span>
            </div>
          ))}
        </div>
      ))}
    </div>
  );
}

const DAY = 24 * 60 * 60 * 1000;

function Ends({ c, now, timezone, short }: { c: EveonlineSkillsCharacter; now: string; timezone: string; short: boolean }) {
  if (c.error && !c.fetchedAt) return <span className={styles.ends}>fout</span>;
  const failed = c.error ? <span className={styles.error}>fout </span> : null;
  if (c.paused) return <span className={styles.ends}>{failed}gepauzeerd</span>;
  if (!c.ends) return <span className={`${styles.ends} ${styles.alert}`}>{failed}leeg</span>;
  const left = Date.parse(c.ends) - Date.parse(now);
  return (
    <span className={`${styles.ends} ${left < DAY ? styles.alert : ""}`}>
      {failed}
      {formatDuration(left)}
      {short ? "" : ` · ${formatDayTime(c.ends, timezone)}`}
    </span>
  );
}
