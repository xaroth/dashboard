import { DeviceStatus } from "./DeviceStatus";
import { HouseScene, SCENE_HEIGHT } from "./HouseScene";
import { StaleMark } from "./StaleMark";
import { WeatherIcon } from "./WeatherIcon";
import type { CalendarUpcoming, EveonlineSkillsCharacter, RssFeed, WeatherForecast } from "../generated/queries";
import { addDays, dayEvents, fit, localDate } from "../lib/calendar";
import { kwhToday } from "../lib/energy";
import { skillLevel } from "../lib/eveonline";
import { formatAge, formatDayShort, formatDuration, formatMillimeters, formatTemperature } from "../lib/format";
import { carStatus, skillWarnings, weekFraction } from "../lib/home";
import { query, type PageProps } from "../lib/page";
import { weatherIcon } from "../lib/weather";
import styles from "./HomeScreen.module.css";

/** What every home screen shows; a layout page exports these as its data, with homeSkills to add the skill queues. */
export const homeData = {
  weather: query("weather.forecast", { hours: 1, days: 5 }),
  meter: query("p1.actual"),
  solar: query("solar.production"),
  // No query returns today's solar energy; it is summed from the power series.
  solarDay: query("solar.power", { hours: 24, stepMinutes: 15 }),
  // The first car; a device picks another with home.car: {id: ...}.
  car: query("car.vehicle"),
  // A device picks the feed with home.news: {feed: ...}.
  news: query("rss.items", { limit: 10 }),
  agenda: query("calendar.upcoming", { days: 5, limit: 60 }),
};

export const homeSkills = {
  queues: query("eveonline_skills.queue", { skills: 1 }),
};

type Base = PageProps<typeof homeData>;

export type HomeScreenProps = Omit<Base, "data"> & {
  /** Without queues the skill block is left out and the headlines take its room. */
  data: Base["data"] & Partial<PageProps<typeof homeSkills>["data"]>;
};

// Rows that fit, in px of the heights in home.module.css.
const NEWS_HEIGHT = 24;
const SKILL_HEIGHT = 22;
const MAX_SKILLS = 3;
// A day card without its events: borders, heading, weather and padding; then each event row with its gap.
const DAY_CHROME = 75;
const EVENT_HEIGHT = 17;
const SCREEN_PAD_Y = 8;
const GAP = 8;

/**
 * The main screen: the house and car on the right, skill queues running out
 * and headlines on the left, and weather with the agenda per day along the
 * bottom; the device's update time, signal and battery top right. A block
 * whose data is stale or missing shows a "!". Laid out for 800 px wide; the
 * day cards take the height left.
 */
export function HomeScreen({ data, device, viewport, now, timezone }: HomeScreenProps) {
  const { portrait } = viewport;
  const m = data.meter.value;
  const s = data.solar.value;
  const f = data.weather.value;
  const v = data.car.value;
  // No car configured is no error: the scene leaves it out.
  const showCar = v != null || data.car.error != null;
  const sunset = f?.daily[0]?.sunset;
  const night = f ? !f.current.isDay : false;

  const queues = data.queues?.value ?? [];
  const warnings = skillWarnings(queues, now).slice(0, MAX_SKILLS);
  const failedChars = queues.some((c) => c.error);
  // The headlines take what the skill queues leave beside the house, or under it in portrait.
  const topHeight = portrait ? 300 : SCENE_HEIGHT;
  const skillsHeight = warnings.length ? warnings.length * SKILL_HEIGHT + 16 : 0;
  const newsRows = Math.max(0, Math.floor((topHeight - skillsHeight - 20) / NEWS_HEIGHT));
  const top = portrait ? SCENE_HEIGHT + GAP + topHeight : SCENE_HEIGHT;
  const daysHeight = viewport.height - 2 * SCREEN_PAD_Y - top - GAP;
  const eventRows = Math.max(1, Math.floor((daysHeight - DAY_CHROME + 2) / EVENT_HEIGHT));

  return (
    <div className={styles.screen}>
      <DeviceStatus wake={device.wake} now={now} timezone={timezone} className={styles.device} />
      <div className={portrait ? styles.topPortrait : styles.top}>
        <div className={styles.left} style={{ height: topHeight }}>
          {warnings.length ? <Skills chars={warnings} now={now} stale={!!data.queues?.stale || failedChars} /> : null}
          <News feed={data.news.value} stale={data.news.stale} rows={newsRows} now={now} />
        </div>
        <HouseScene
          solarW={s?.powerW ?? null}
          solarKwh={kwhToday(data.solarDay.value ?? [], now, timezone)}
          night={night}
          takenKwh={m?.today?.takenKwh ?? null}
          returnedKwh={m?.today?.returnedKwh ?? null}
          flowKw={m?.flowKw ?? null}
          car={v ? carStatus(v, now, timezone) : null}
          showCar={showCar}
          stale={{ solar: data.solar.stale || data.solarDay.stale, meter: data.meter.stale, car: data.car.stale }}
        />
      </div>
      <Days
        forecast={f}
        weatherStale={data.weather.stale}
        agenda={data.agenda.value}
        agendaStale={data.agenda.stale || (data.agenda.value?.calendars.some((c) => c.error) ?? false)}
        count={portrait ? 3 : 5}
        eventRows={eventRows}
        now={now}
        timezone={timezone}
        afterSunset={sunset != null && Date.parse(now) >= Date.parse(sunset)}
      />
    </div>
  );
}

function Skills({ chars, now, stale }: { chars: EveonlineSkillsCharacter[]; now: string; stale: boolean }) {
  return (
    <div className={styles.skills}>
      {chars.map((c) => {
        const skill = c.skills[0];
        return (
          <div key={c.id} className={styles.skill}>
            <span className={styles.skillName}>{c.name}</span>
            <div className={styles.track}>
              <div className={c.paused ? styles.trackFillPaused : styles.trackFill} style={{ width: `${Math.round(weekFraction(c, now) * 100)}%` }} />
            </div>
            <span className={styles.skillNow}>{c.paused ? "gepauzeerd" : skill ? `${skill.name || `Skill ${skill.id}`} ${skillLevel(skill.level)}` : "leeg"}</span>
            <span className={styles.skillLeft}>{c.ends && !c.paused ? formatDuration(Date.parse(c.ends) - Date.parse(now)) : "-"}</span>
          </div>
        );
      })}
      {stale ? <StaleMark className={styles.staleSkills} /> : null}
    </div>
  );
}

function News({ feed, stale, rows, now }: { feed: RssFeed | null; stale: boolean; rows: number; now: string }) {
  const items = feed?.items.slice(0, rows) ?? [];
  return (
    <div className={styles.news}>
      <div className={styles.newsHead}>
        <span className={styles.newsTitle}>{feed ? feed.title || feed.name : "Nieuws"}</span>
        {stale || !feed || feed.error ? <StaleMark className={styles.staleNews} /> : null}
      </div>
      {items.map((item, i) => (
        <div key={`${i}-${item.link}`} className={styles.newsItem}>
          <span className={styles.newsItemTitle}>{item.title}</span>
          <span className={styles.newsAge}>{item.published ? formatAge(item.published, now) : ""}</span>
        </div>
      ))}
    </div>
  );
}

type DaysProps = {
  forecast: WeatherForecast | null;
  weatherStale: boolean;
  agenda: CalendarUpcoming | null;
  agendaStale: boolean;
  count: number;
  eventRows: number;
  now: string;
  timezone: string;
  afterSunset: boolean;
};

function Days({ forecast, weatherStale, agenda, agendaStale, count, eventRows, now, timezone, afterSunset }: DaysProps) {
  const today = localDate(now, timezone);
  return (
    <div className={styles.days}>
      {Array.from({ length: count }, (_, i) => {
        const date = addDays(today, i);
        const day = forecast?.daily.find((d) => d.date === date);
        // After sunset the rest of today is the evening: the temperature now says more than the day's range.
        const evening = i === 0 && afterSunset && forecast != null;
        const { shown, more } = fit(agenda ? dayEvents(agenda.events, date, now, timezone) : [], eventRows);
        return (
          <div key={date} className={styles.day}>
            <div className={i === 0 ? styles.dayHeadToday : styles.dayHead}>
              <span>{formatDayShort(`${date}T12:00:00Z`, timezone)}</span>
              {i === 0 ? <span>vandaag</span> : null}
            </div>
            <div className={styles.weather}>
              {evening ? (
                <>
                  <WeatherIcon name={weatherIcon(forecast!.current.weatherCode, forecast!.current.isDay)} size={34} />
                  <div className={styles.temps}>
                    <span className={styles.tempMax}>{formatTemperature(forecast!.current.temperature, 0)}</span>
                    <span className={styles.tempMin}>nu</span>
                  </div>
                </>
              ) : day ? (
                <>
                  <WeatherIcon name={weatherIcon(day.weatherCode)} size={34} />
                  <div className={styles.temps}>
                    <span className={styles.tempMax}>{formatTemperature(day.temperatureMax, 0)}</span>
                    <span className={styles.tempMin}>{formatTemperature(day.temperatureMin, 0)}</span>
                  </div>
                </>
              ) : (
                <span className={styles.noWeather}>-</span>
              )}
              {day ? (
                <div className={styles.rain}>
                  <svg width={10} height={12} viewBox="0 0 10 12">
                    <path d="M5 0C3 4 1 6 1 8a4 4 0 0 0 8 0c0-2-2-4-4-8z" fill="#000" />
                  </svg>
                  {day.precipitationProbabilityMax != null ? <span>{day.precipitationProbabilityMax}%</span> : null}
                  {day.precipitationSum >= 0.5 ? <span>{formatMillimeters(day.precipitationSum)}</span> : null}
                </div>
              ) : null}
              {i === 0 && weatherStale ? <StaleMark className={styles.staleWeather} /> : null}
            </div>
            <div className={styles.events}>
              {shown.map((e, j) =>
                e.time == null ? (
                  <div key={j} className={styles.eventAllDay}>
                    <span className={styles.eventTitle}>{e.title}</span>
                  </div>
                ) : (
                  <div key={j} className={styles.event}>
                    <span className={styles.eventTime}>{e.time}</span>
                    <span className={styles.eventTitle}>{e.title}</span>
                  </div>
                ),
              )}
              {more > 0 ? <span className={styles.more}>+{more} meer</span> : null}
              {agenda && shown.length === 0 ? <span className={styles.none}>geen afspraken</span> : null}
              {i === 0 && (agendaStale || !agenda) ? <StaleMark className={styles.staleAgenda} /> : null}
            </div>
          </div>
        );
      })}
    </div>
  );
}
