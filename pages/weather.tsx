import { LineChart } from "./components/LineChart";
import { formatDayShort, formatMillimeters, formatTemperature, formatTime } from "./lib/format";
import { query, type PageProps } from "./lib/page";
import { weatherLabel, windDirection } from "./lib/weather";
import styles from "./weather.module.css";

export const data = {
  weather: query("weather.forecast", { hours: 24, days: 3 }),
};

type Data = typeof data;

export default function Weather({ data, viewport, timezone }: PageProps<Data>) {
  const { value: f, fetchedAt, stale, error } = data.weather;
  if (!f) {
    return (
      <div className={styles.screen}>
        <div className={styles.missing}>Geen weerbericht{error ? `: ${error}` : ""}</div>
      </div>
    );
  }
  const chartWidth = viewport.width - 64;
  const hourLabels = f.hourly.filter((_, i) => i % 6 === 0);
  return (
    <div className={styles.screen}>
      <div className={styles.header}>
        <span>{f.location.name}</span>
        <span className={stale ? styles.stale : undefined}>
          {stale ? "Verouderd, " : ""}bijgewerkt {fetchedAt ? formatTime(fetchedAt, timezone) : "-"}
        </span>
      </div>
      <div className={styles.now}>
        <div className={styles.temperature}>{formatTemperature(f.current.temperature)}</div>
        <div className={styles.details}>
          <div className={styles.label}>{weatherLabel(f.current.weatherCode)}</div>
          <div>Gevoel {formatTemperature(f.current.apparent)}</div>
          <div>
            Wind {Math.round(f.current.windSpeed)} km/u {windDirection(f.current.windDirection)}
          </div>
        </div>
      </div>
      <div className={styles.chart}>
        <LineChart
          values={f.hourly.map((h) => h.temperature)}
          bars={f.hourly.map((h) => h.precipitationProbability)}
          width={chartWidth}
          height={Math.round(viewport.height * 0.22)}
        />
        <div className={styles.hours}>
          {hourLabels.map((h) => (
            <span key={h.time}>
              {formatTime(h.time, timezone)} {formatTemperature(h.temperature, 0)}
            </span>
          ))}
        </div>
      </div>
      <div className={styles.days}>
        {f.daily.map((d) => (
          <div key={d.date} className={styles.day}>
            <span className={styles.dayName}>{formatDayShort(d.date, timezone)}</span>
            <span>{weatherLabel(d.weatherCode)}</span>
            <span>
              {formatTemperature(d.temperatureMax, 0)} / {formatTemperature(d.temperatureMin, 0)}
            </span>
            <span>
              {formatMillimeters(d.precipitationSum)}
              {d.precipitationProbabilityMax != null ? `, ${d.precipitationProbabilityMax}%` : ""}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}
