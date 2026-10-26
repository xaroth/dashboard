import { HEADER_HEIGHT, Missing, PAD_Y, PageFrame, Updated } from "../components/PageFrame";
import { agendaDays, dayLabel, eventTime, fitRows } from "../lib/calendar";
import { query, type PageProps } from "../lib/page";
import styles from "./calendar.module.css";

export const data = {
  events: query("calendar.upcoming", { days: 14, limit: 30 }),
};

type Data = typeof data;

// In px; they match calendar.module.css.
const GAP = 12;
const ROW = 38;
const ERROR_ROW = 26;

export default function Calendar({ data, viewport, now, timezone }: PageProps<Data>) {
  const { value: agenda, error } = data.events;
  const status = <Updated result={data.events} timezone={timezone} showError />;
  if (!agenda) {
    return (
      <PageFrame title="Agenda">
        <Missing what="agenda" error={error} />
      </PageFrame>
    );
  }
  const failed = agenda.calendars.filter((c) => c.error);
  const height = viewport.height - 2 * PAD_Y - HEADER_HEIGHT - GAP - (failed.length ? GAP + ERROR_ROW : 0);
  const days = fitRows(agendaDays(agenda.events, now, timezone), Math.floor(height / ROW));
  const named = agenda.calendars.length > 1;
  return (
    <PageFrame title="Agenda" status={status} gap={GAP}>
      {days.length === 0 ? (
        <Missing what="afspraken" />
      ) : (
        <div className={styles.agenda}>
          {days.map((d) => (
            <div key={d.date} className={styles.day}>
              <div className={styles.date}>{dayLabel(d.date, now, timezone)}</div>
              {d.events.map((e, i) => (
                <div key={i} className={styles.event}>
                  <span className={styles.time}>{eventTime(e, d.date, timezone)}</span>
                  <span className={styles.title}>
                    {e.title || "(geen titel)"}
                    {e.location ? <span className={styles.location}>{e.location}</span> : null}
                  </span>
                  {named ? <span className={styles.calendar}>{e.calendar}</span> : null}
                </div>
              ))}
            </div>
          ))}
        </div>
      )}
      {failed.length ? <div className={styles.errors}>{failed.map((c) => `${c.name}: ${c.error}`).join(" · ")}</div> : null}
    </PageFrame>
  );
}
