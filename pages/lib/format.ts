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
