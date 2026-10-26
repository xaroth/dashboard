import { HEADER_HEIGHT, Missing, PAD_Y, PageFrame, Updated } from "../components/PageFrame";
import { StaleMark } from "../components/StaleMark";
import { WeatherIcon } from "../components/WeatherIcon";
import type { DayEvent } from "../lib/calendar";
import { addDays, dayEvents, fit, isWeekend, localDate, shortDate } from "../lib/calendar";
import { formatDayShort, formatTemperature } from "../lib/format";
import { query, type PageProps } from "../lib/page";
import { weatherIcon } from "../lib/weather";
import styles from "./calendar.module.css";

export const data = {
  events: query("calendar.upcoming", { days: 15, limit: 100 }),
  weather: query("weather.forecast", { hours: 1, days: 7 }),
};

type Data = typeof data;

// In px; they match calendar.module.css.
const GAP = 8;
const CELL_GAP = 6;
const CELL_CHROME = 2 * 2 + 20 + 2 + 5; // borders, day heading and its rule, event padding
const EVENT_ROW = 16;

/**
 * The next 15 days from today as a grid of day cells, 5 by 3 in landscape and
 * 3 by 5 in portrait: the day's weather in its heading, then all-day events
 * as bars and timed events with their start. Muted calendars are gray.
 */
export default function Calendar({ data, viewport, now, timezone }: PageProps<Data>) {
  const { value: agenda, error } = data.events;
  if (!agenda) {
    return (
      <PageFrame title="Agenda">
        <Missing what="agenda" error={error} />
      </PageFrame>
    );
  }
  const [columns, rows] = viewport.portrait ? [3, 5] : [5, 3];
  const height = viewport.height - 2 * PAD_Y - HEADER_HEIGHT - GAP;
  const eventRows = Math.max(1, Math.floor(((height - (rows - 1) * CELL_GAP) / rows - CELL_CHROME) / EVENT_ROW));
  const today = localDate(now, timezone);
  const dates = Array.from({ length: columns * rows }, (_, i) => addDays(today, i));
  const muted = new Set(agenda.calendars.filter((c) => c.muted).map((c) => c.name));
  const failed = agenda.calendars.some((c) => c.error);
  const daily = data.weather.value?.daily ?? [];
  return (
    <PageFrame
      title={
        <span>
          <span className={styles.title}>Agenda</span>
          {/* The range does not fit next to the update time in portrait. */}
          {viewport.portrait ? null : ` · ${shortDate(today)} t/m ${shortDate(dates.at(-1)!)}`}
        </span>
      }
      status={
        <span className={styles.status}>
          {failed ? <StaleMark inline /> : null}
          <Updated result={data.events} timezone={timezone} />
        </span>
      }
      gap={GAP}
    >
      <div className={styles.grid} style={{ gridTemplateColumns: `repeat(${columns}, 1fr)`, gridTemplateRows: `repeat(${rows}, 1fr)` }}>
        {dates.map((date, i) => {
          const weather = daily.find((d) => d.date === date);
          const { shown, more } = fit(dayEvents(agenda.events, date, now, timezone), eventRows);
          return (
            <div key={date} className={styles.cell}>
              <div className={i === 0 ? styles.headToday : isWeekend(date) ? styles.headWeekend : styles.head}>
                <span>{formatDayShort(`${date}T12:00:00Z`, timezone)}</span>
                {weather ? (
                  <span className={styles.weather}>
                    <WeatherIcon name={weatherIcon(weather.weatherCode)} size={16} inverted={i === 0} />
                    {formatTemperature(weather.temperatureMax, 0)}
                  </span>
                ) : null}
              </div>
              <div className={styles.events}>
                {shown.map((e, j) => (
                  <Event key={j} e={e} muted={muted.has(e.calendar)} />
                ))}
                {more > 0 ? <span className={styles.more}>+{more} meer</span> : null}
              </div>
            </div>
          );
        })}
      </div>
    </PageFrame>
  );
}

function Event({ e, muted }: { e: DayEvent; muted: boolean }) {
  if (e.time == null) {
    // Square ends where the event runs on from the day before or into the next, so it reads as one band.
    const [l, r] = [e.before ? 0 : 3, e.after ? 0 : 3];
    return (
      <div
        className={muted || e.before ? styles.barQuiet : styles.bar}
        style={{ borderRadius: `${l}px ${r}px ${r}px ${l}px`, marginLeft: e.before ? 0 : 3, marginRight: e.after ? 0 : 3 }}
      >
        {e.title}
      </div>
    );
  }
  return (
    <div className={muted ? styles.eventMuted : styles.event}>
      <span className={styles.time}>{e.time}</span>
      <span className={styles.eventTitle}>{e.title}</span>
    </div>
  );
}
