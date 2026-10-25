import type { SeriesPoint } from "../generated/queries";

export type Scale = (v: number) => number;

/** Maps [d0, d1] linearly onto [r0, r1]. */
export function linear(d0: number, d1: number, r0: number, r1: number): Scale {
  return (v) => r0 + ((v - d0) / (d1 - d0)) * (r1 - r0);
}

/** Maps [min, max] onto a chart height, max at the top, inset by half the stroke so a line along an edge is not clipped. */
export function yScale(min: number, max: number, height: number, stroke: number): Scale {
  return linear(min, max, height - stroke / 2, stroke / 2);
}

/** "x,y x,y" for an SVG points attribute, to 0.1 px. */
export function svgPoints(points: [number, number][]): string {
  return points.map(([x, y]) => `${x.toFixed(1)},${y.toFixed(1)}`).join(" ");
}

/** The range of values, padded by a fraction of it but at least minPad, so a line never touches the edges. */
export function paddedRange(values: number[], fraction: number, minPad: number): [number, number] {
  const min = Math.min(...values);
  const max = Math.max(...values);
  const pad = Math.max((max - min) * fraction, minPad);
  return [min - pad, max + pad];
}

/** Splits points into runs without gaps, so charts break where data is missing. */
export function runs(points: SeriesPoint[]): { t: number; v: number }[][] {
  const out: { t: number; v: number }[][] = [];
  let run: { t: number; v: number }[] = [];
  for (const p of points) {
    if (p.v == null) {
      if (run.length) out.push(run);
      run = [];
      continue;
    }
    run.push({ t: Date.parse(p.t), v: p.v });
  }
  if (run.length) out.push(run);
  return out;
}
