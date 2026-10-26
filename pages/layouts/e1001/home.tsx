import { DeviceStatus } from "../../components/DeviceStatus";
import { HouseScene } from "../../components/HouseScene";
import { StaleMark } from "../../components/StaleMark";
import { WeatherIcon } from "../../components/WeatherIcon";
import type { CalendarUpcoming, EveonlineSkillsCharacter, RssFeed, WeatherForecast } from "../../generated/queries";
import { addDays, localDate } from "../../lib/calendar";
import { kwhToday } from "../../lib/energy";
import { skillLevel } from "../../lib/eveonline";
import { formatAge, formatDayShort, formatDuration, formatMillimeters, formatTemperature } from "../../lib/format";
import { carStatus, dayEvents, fit, skillWarnings, weekFraction } from "../../lib/home";
import { query, type PageProps } from "../../lib/page";
import { weatherIcon } from "../../lib/weather";
import styles from "./home.module.css";

export const data = {
  weather: query("weather.forecast", { hours: 1, days: 5 }),
  meter: query("p1.actual"),
  solar: query("solar.production"),
  // No query returns today's solar energy; it is summed from the power series.
  solarDay: query("solar.power", { hours: 24, stepMinutes: 15 }),
  // The first car; a device picks another with home.car: {id: ...}.
  car: query("car.vehicle"),
  queues: query("eveonline_skills.queue", { skills: 1 }),
  // A device picks the feed with home.news: {feed: ...}.
  news: query("rss.items", { limit: 10 }),
  agenda: query("calendar.upcoming", { days: 5, limit: 60 }),
};

type Data = typeof data;

// Rows that fit, in px of the heights in home.module.css.
const NEWS_HEIGHT = 24;
const SKILL_HEIGHT = 22;
const MAX_SKILLS = 3;
const EVENT_ROWS = 6;

/**
 * The main page on the E1001: the house and car on the right, skill queues
 * running out and headlines on the left, and weather with the agenda per day
 * along the bottom; the device's update time, signal and battery top right. A block whose data is stale or missing shows a "!".
 */
export default function Home({ data, device, viewport, now, timezone }: PageProps<Data>) {
  const { portrait } = viewport;
  const m = data.meter.value;
  const s = data.solar.value;
  const f = data.weather.value;
  const v = data.car.value;
  // No car configured is no error: the scene leaves it out.
  const showCar = v != null || data.car.error != null;
  const sunset = f?.daily[0]?.sunset;
  const night = f ? !f.current.isDay : false;

  const warnings = skillWarnings(data.queues.value ?? [], now).slice(0, MAX_SKILLS);
  const failedChars = (data.queues.value ?? []).some((c) => c.error);
  // The top block is 272 px high in landscape; the headlines take what the skill queues leave.
  const topHeight = portrait ? 300 : 272;
  const skillsHeight = warnings.length ? warnings.length * SKILL_HEIGHT + 16 : 0;
  const newsRows = Math.max(0, Math.floor((topHeight - skillsHeight - 20) / NEWS_HEIGHT));

  return (
    <div className={styles.screen}>
      <DeviceStatus wake={device.wake} now={now} timezone={timezone} className={styles.device} />
      <div className={portrait ? styles.topPortrait : styles.top}>
        <div className={styles.left} style={{ height: topHeight }}>
          {warnings.length ? <Skills chars={warnings} now={now} stale={data.queues.stale || failedChars} /> : null}
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
  now: string;
  timezone: string;
  afterSunset: boolean;
};

function Days({ forecast, weatherStale, agenda, agendaStale, count, now, timezone, afterSunset }: DaysProps) {
  const today = localDate(now, timezone);
  return (
    <div className={styles.days}>
      {Array.from({ length: count }, (_, i) => {
        const date = addDays(today, i);
        const day = forecast?.daily.find((d) => d.date === date);
        // After sunset the rest of today is the evening: the temperature now says more than the day's range.
        const evening = i === 0 && afterSunset && forecast != null;
        const { shown, more } = fit(agenda ? dayEvents(agenda.events, date, now, timezone) : [], EVENT_ROWS);
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
