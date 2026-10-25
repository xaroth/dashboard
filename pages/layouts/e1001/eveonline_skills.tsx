import { HEADER_HEIGHT, Missing, PAD_X, PAD_Y, PageFrame, Updated } from "../../components/PageFrame";
import { SkillQueues } from "../../components/SkillQueues";
import { query, type PageProps } from "../../lib/page";

export const data = {
  queues: query("eveonline_skills.queue", { skills: 20 }),
};

type Data = typeof data;

const GAP = 20;

export default function EveOnlineSkills({ data, viewport, now, timezone }: PageProps<Data>) {
  const { value: queues, error } = data.queues;
  return (
    <PageFrame title="EVE Online · Skillqueues" status={<Updated result={data.queues} timezone={timezone} showError />} gap={GAP}>
      {!queues && error ? (
        <Missing what="skillqueues" />
      ) : (
        <SkillQueues
          characters={queues ?? []}
          now={now}
          timezone={timezone}
          width={viewport.width - 2 * PAD_X}
          height={viewport.height - 2 * PAD_Y - HEADER_HEIGHT - GAP}
        />
      )}
    </PageFrame>
  );
}
