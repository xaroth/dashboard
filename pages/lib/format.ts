export function formatDateTime(iso: string, timezone: string): string {
  return new Intl.DateTimeFormat("nl-NL", {
    timeZone: timezone,
    weekday: "long",
    day: "numeric",
    month: "long",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(iso));
}

const number1 = new Intl.NumberFormat("nl-NL", { minimumFractionDigits: 1, maximumFractionDigits: 1 });
const number0 = new Intl.NumberFormat("nl-NL", { maximumFractionDigits: 0 });

/** 16,6° */
export function formatTemperature(celsius: number, digits: 0 | 1 = 1): string {
  // Round first, so -0.04 is "0,0°" rather than "-0,0°".
  const rounded = Number(celsius.toFixed(digits)) || 0;
  return `${(digits === 1 ? number1 : number0).format(rounded)}°`;
}

/** 07:30 */
export function formatTime(iso: string, timezone: string): string {
  return new Intl.DateTimeFormat("nl-NL", { timeZone: timezone, hour: "2-digit", minute: "2-digit" }).format(new Date(iso));
}

/** vr 16 */
export function formatDayShort(iso: string, timezone: string): string {
  return new Intl.DateTimeFormat("nl-NL", { timeZone: timezone, weekday: "short", day: "numeric" }).format(new Date(iso));
}

/** 2,4 mm */
export function formatMillimeters(mm: number): string {
  return `${number1.format(mm)} mm`;
}

/** 26.017 */
export function formatCount(n: number): string {
  return number0.format(n);
}

/** 2 d 3 u, 5 u 12 m, 12 m; rounded down to the minute. */
export function formatDuration(ms: number): string {
  const minutes = Math.max(0, Math.floor(ms / 60_000));
  const d = Math.floor(minutes / 1440);
  const h = Math.floor((minutes % 1440) / 60);
  const m = minutes % 60;
  if (d > 0) return `${d} d ${h} u`;
  if (h > 0) return `${h} u ${m} m`;
  return `${m} m`;
}

/** vr 16 07:30 */
export function formatDayTime(iso: string, timezone: string): string {
  return `${formatDayShort(iso, timezone)} ${formatTime(iso, timezone)}`;
}
