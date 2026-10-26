const numbers = [0, 1, 2].map(
  (digits) => new Intl.NumberFormat("nl-NL", { minimumFractionDigits: digits, maximumFractionDigits: digits }),
);

/** n rounded to digits; -0.004 to 2 digits is 0, not -0. */
export function round(n: number, digits: number): number {
  return Number(n.toFixed(digits)) || 0;
}

/** 1.234,5 with digits decimals, rounded first so it never shows -0. */
export function formatNumber(n: number, digits: 0 | 1 | 2): string {
  return numbers[digits]!.format(round(n, digits));
}

// Intl.DateTimeFormat is slow to construct, so each format is built once per timezone.
function zoned(options: Intl.DateTimeFormatOptions): (timezone: string) => Intl.DateTimeFormat {
  const byZone = new Map<string, Intl.DateTimeFormat>();
  return (timeZone) => {
    let f = byZone.get(timeZone);
    if (!f) byZone.set(timeZone, (f = new Intl.DateTimeFormat("nl-NL", { ...options, timeZone })));
    return f;
  };
}

const dateTime = zoned({ weekday: "long", day: "numeric", month: "long", hour: "2-digit", minute: "2-digit" });
const time = zoned({ hour: "2-digit", minute: "2-digit" });
const dayShort = zoned({ weekday: "short", day: "numeric" });
const clock = zoned({ hour: "numeric", minute: "numeric", hourCycle: "h23" });

/** vrijdag 16 oktober om 07:30 */
export function formatDateTime(iso: string, timezone: string): string {
  return dateTime(timezone).format(new Date(iso));
}

/** 16,6° */
export function formatTemperature(celsius: number, digits: 0 | 1 = 1): string {
  return `${formatNumber(celsius, digits)}°`;
}

/** 07:30 */
export function formatTime(iso: string, timezone: string): string {
  return time(timezone).format(new Date(iso));
}

/** vr 16 */
export function formatDayShort(iso: string, timezone: string): string {
  return dayShort(timezone).format(new Date(iso));
}

/** 2,4 mm */
export function formatMillimeters(mm: number): string {
  return `${formatNumber(mm, 1)} mm`;
}

/** 26.017 */
export function formatCount(n: number): string {
  return formatNumber(n, 0);
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

/** Local wall-clock hour and minute of t, in ms since the epoch. */
export function localClock(t: number, timezone: string): { hour: number; minute: number } {
  let hour = 0;
  let minute = 0;
  for (const p of clock(timezone).formatToParts(t)) {
    if (p.type === "hour") hour = Number(p.value);
    else if (p.type === "minute") minute = Number(p.value);
  }
  return { hour, minute };
}

/** How long ago iso was, in its largest unit: 12 m, 3 u, 2 d. */
export function formatAge(iso: string, now: string): string {
  const minutes = Math.max(0, Math.floor((Date.parse(now) - Date.parse(iso)) / 60_000));
  if (minutes < 60) return `${minutes} m`;
  if (minutes < 1440) return `${Math.floor(minutes / 60)} u`;
  return `${Math.floor(minutes / 1440)} d`;
}
