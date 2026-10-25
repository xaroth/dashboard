import type { SeriesPoint } from "../generated/queries";
import { formatNumber, localClock, round } from "./format";

/** +1,23 kW taken from the net, -0,45 kW returned to it. */
export function formatFlow(kw: number): string {
  return `${round(kw, 2) > 0 ? "+" : ""}${formatNumber(kw, 2)} kW`;
}

/** Which way flow goes, as formatFlow shows it: a flow that shows as 0,00 kW is Afname. */
export function flowDirection(kw: number): "Afname" | "Teruglevering" {
  return round(kw, 2) < 0 ? "Teruglevering" : "Afname";
}

/** 1.234 W */
export function formatWatt(w: number): string {
  return `${formatNumber(w, 0)} W`;
}

/** 12,3 kWh */
export function formatKwh(kwh: number): string {
  return `${formatNumber(kwh, 1)} kWh`;
}

/** 1,23 m³; "-" while gas is unknown. */
export function formatGas(m3: number | null): string {
  return m3 == null ? "-" : `${formatNumber(m3, 2)} m³`;
}

/** 230,1 V; "-" for a phase the meter does not report. */
export function formatVolt(v: number | null): string {
  return v == null ? "-" : `${formatNumber(v, 1)} V`;
}

/** 1,2 kW from watts, for axis labels. */
export function formatKwAxis(w: number): string {
  return `${formatNumber(w / 1000, 1)} kW`;
}

/** 456.1: the inverter serial's last three digits and the input. */
export function panelLabel(inverter: string, port: number): string {
  return `${inverter.slice(-3)}.${port}`;
}

/** Whether a day's usage starts more than 5 minutes after local midnight, i.e. its first reading came late. */
export function startedLate(fromIso: string, timezone: string): boolean {
  const { hour, minute } = localClock(Date.parse(fromIso), timezone);
  return hour * 60 + minute > 5;
}

const MINUTE = 60_000;
const DEFAULT_STEP = 15 * MINUTE;

/** What EnergyChart spans: times in ms, the bucket size, and values in W. */
export type ChartDomain = { start: number; end: number; step: number; min: number; max: number };

/**
 * The domain covering flow (kW) and solar (W), or null when both are empty.
 * The series may start and end at different times; the step is the smaller of theirs.
 */
export function chartDomain(flow: SeriesPoint[], solar: SeriesPoint[]): ChartDomain | null {
  const series = [flow, solar].filter((s) => s.length > 0);
  if (series.length === 0) return null;
  const steps = series.filter((s) => s.length > 1).map((s) => Date.parse(s[1]!.t) - Date.parse(s[0]!.t));
  const step = steps.length ? Math.min(...steps) : DEFAULT_STEP;
  const start = Math.min(...series.map((s) => Date.parse(s[0]!.t)));
  const end = Math.max(...series.map((s) => Date.parse(s[s.length - 1]!.t))) + step;
  const watts = [
    ...solar.flatMap((p) => (p.v == null ? [] : [p.v])),
    ...flow.flatMap((p) => (p.v == null ? [] : [p.v * 1000])),
  ];
  // At least 1 kW up and the zero line in view.
  const max = Math.max(1000, ...watts) * 1.05;
  const min = Math.min(0, ...watts) * 1.05;
  return { start, end, step, min, max };
}

/** Times in [start, end) at local whole hours divisible by every; quarter hours are checked for zones with a half-hour offset. */
export function hourTicks(start: number, end: number, timezone: string, every = 6): number[] {
  const quarter = 15 * MINUTE;
  const ticks: number[] = [];
  for (let t = Math.ceil(start / quarter) * quarter; t < end; t += quarter) {
    const { hour, minute } = localClock(t, timezone);
    if (minute === 0 && hour % every === 0) ticks.push(t);
  }
  return ticks;
}
