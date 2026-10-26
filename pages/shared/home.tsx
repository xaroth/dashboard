import { PageFrame } from "../components/PageFrame";
import { SummaryRow } from "../components/SummaryRow";
import { formatFlow, formatWatt, kwhToday } from "../lib/energy";
import { formatCount, formatDateTime, formatDayTime, formatDuration, formatMillimeters, formatNumber, formatTemperature } from "../lib/format";
import { carsSummary, combine, nextEvent, soonestQueue } from "../lib/home";
import { query, type PageProps } from "../lib/page";
import { weatherLabel } from "../lib/weather";
import styles from "./home.module.css";

export const data = {
  weather: query("weather.forecast", { hours: 1, days: 1 }),
  meter: query("p1.actual"),
  solar: query("solar.production"),
  // No query returns today's solar energy; it is summed from the power series.
  solarDay: query("solar.power", { hours: 24, stepMinutes: 15 }),
  cars: query("car.vehicles"),
  server: query("eveonline_status.server"),
  queues: query("eveonline_skills.queue", { skills: 1 }),
  news: query("rss.items", { limit: 1 }),
  agenda: query("calendar.upcoming", { days: 2, limit: 20 }),
};

type Data = typeof data;

const join = (parts: (string | null | undefined | false)[]) => parts.filter(Boolean).join(" · ") || null;

/** The key value of every source, one row each, so a failing source is one row and not the page. */
export default function Home({ data, viewport, now, timezone }: PageProps<Data>) {
  const stacked = viewport.portrait;
  const row = { timezone, stacked };

  const f = data.weather.value;
  const today = f?.daily[0];
  const weather = f
    ? {
        value: `${formatTemperature(f.current.temperature)} ${weatherLabel(f.current.weatherCode).toLowerCase()}`,
        detail: today
          ? join([
              `${formatTemperature(today.temperatureMax, 0)} / ${formatTemperature(today.temperatureMin, 0)}`,
              `${formatMillimeters(today.precipitationSum)}${today.precipitationProbabilityMax != null ? `, ${today.precipitationProbabilityMax}%` : ""}`,
            ])
          : null,
      }
    : null;

  const m = data.meter.value;
  const s = data.solar.value;
  const solarKwh = kwhToday(data.solarDay.value ?? [], now, timezone);
  const kwh = [
    ...(m?.today ? [`afname ${formatNumber(m.today.takenKwh, 1)}`, `terug ${formatNumber(m.today.returnedKwh, 1)}`] : []),
    ...(solarKwh != null ? [`zon ${formatNumber(solarKwh, 1)}`] : []),
  ];
  const energy = {
    value: join([m && `${formatFlow(m.flowKw)} net`, s && `${formatWatt(s.powerW)} zon`]),
    detail: kwh.length ? `Vandaag ${kwh.join(", ")} kWh` : null,
  };

  const cars = data.cars.value?.length ? carsSummary(data.cars.value, now, timezone) : null;

  const server = data.server.value;
  const { first: queue, failed } = soonestQueue(data.queues.value ?? []);
  const queueValue = queue
    ? `${queue.name}: ${queue.paused ? "gepauzeerd" : queue.ends ? formatDuration(Date.parse(queue.ends) - Date.parse(now)) : "leeg"}`
    : null;

  const feed = data.news.value;
  const item = feed?.items[0];
  // The update can succeed while this feed failed; its own time and error count.
  const newsStatus = feed ? { fetchedAt: feed.fetchedAt, stale: data.news.stale || feed.error !== "", error: feed.error || data.news.error } : data.news;

  const agenda = data.agenda.value;
  const next = agenda ? nextEvent(agenda.events, now, timezone) : null;
  const failedCalendars = agenda?.calendars.filter((c) => c.error).length ?? 0;

  return (
    <PageFrame title={formatDateTime(now, timezone)}>
      <div className={styles.rows}>
        <SummaryRow {...row} label="Weer" what="weerbericht" status={data.weather} value={weather?.value} detail={weather?.detail} />
        <SummaryRow
          {...row}
          label="Energie"
          what="energiegegevens"
          status={combine(data.meter, data.solar, data.solarDay)}
          value={energy.value}
          detail={energy.detail}
        />
        <SummaryRow {...row} label={cars?.label ?? "Auto's"} what={cars ? "laadstand" : "auto's"} status={data.cars} value={cars?.value} detail={cars?.detail} prose={(data.cars.value?.length ?? 0) > 1} />
        <SummaryRow
          {...row}
          label="EVE server"
          what="serverstatus"
          status={data.server}
          value={server ? join([server.online ? "Online" : "Offline", server.online && `${formatCount(server.players)} spelers`]) : null}
          detail={server?.vip ? "VIP" : null}
        />
        <SummaryRow
          {...row}
          label="Skillqueue"
          what="skillqueues"
          status={data.queues}
          value={queueValue}
          detail={join([queue?.ends && !queue.paused && `Loopt af ${formatDayTime(queue.ends, timezone)}`, failed > 0 && `${failed} met fout`])}
        />
        <SummaryRow
          {...row}
          label="Nieuws"
          what="nieuws"
          status={newsStatus}
          value={item?.title}
          detail={join([item?.published && formatDayTime(item.published, timezone), feed && (feed.title || feed.name)])}
          prose
        />
        <SummaryRow
          {...row}
          label="Agenda"
          what="afspraken vandaag of morgen"
          status={data.agenda}
          value={next?.value}
          detail={join([next?.detail, failedCalendars > 0 && `${failedCalendars} agenda met fout`])}
          prose
        />
      </div>
    </PageFrame>
  );
}
