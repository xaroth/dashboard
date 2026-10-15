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
  return `${(digits === 1 ? number1 : number0).format(celsius)}°`;
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
