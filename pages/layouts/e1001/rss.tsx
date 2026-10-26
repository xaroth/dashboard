import { Missing, PageFrame, Updated } from "../../components/PageFrame";
import { formatDayTime } from "../../lib/format";
import { query, type PageProps } from "../../lib/page";
import styles from "./rss.module.css";

// A device picks its feed with params: {rss.feed: {feed: tech}}.
export const data = {
  feed: query("rss.items", { limit: 8 }),
};

type Data = typeof data;

export default function Rss({ data, timezone }: PageProps<Data>) {
  const { value: feed, error } = data.feed;
  if (!feed) {
    return (
      <PageFrame>
        <Missing what="nieuws" error={error} />
      </PageFrame>
    );
  }
  // The update can succeed while this feed failed; its own time and error are what count.
  const result = { ...data.feed, fetchedAt: feed.fetchedAt, stale: data.feed.stale || feed.error !== "", error: feed.error || error };
  return (
    <PageFrame title={feed.title || feed.name} status={<Updated result={result} timezone={timezone} showError />} gap={16}>
      {feed.items.length === 0 ? (
        <Missing what="berichten" />
      ) : (
        <div className={styles.items}>
          {feed.items.map((item, i) => (
            <div key={`${i}-${item.link}`} className={styles.item}>
              <div className={styles.title}>{item.title}</div>
              <div className={styles.meta}>
                {item.published ? <span className={styles.time}>{formatDayTime(item.published, timezone)}</span> : null}
                <span className={styles.summary}>{item.summary}</span>
              </div>
            </div>
          ))}
        </div>
      )}
    </PageFrame>
  );
}
