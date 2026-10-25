import type { SeriesPoint } from "../generated/queries";
import { linear, runs, svgPoints, yScale } from "../lib/chart";
import type { ChartDomain } from "../lib/energy";

export type EnergyChartProps = {
  /** Solar power in W, drawn as a gray area. */
  solar: SeriesPoint[];
  /** Net flow in kW, + taken, - returned, drawn as a line. */
  flow: SeriesPoint[];
  /** Time range in ms with the bucket size, values sit at bucket centers; value range in W. */
  domain: ChartDomain;
  width: number;
  height: number;
};

/** Solar production and net flow over time, as inline SVG with a zero line. */
export function EnergyChart({ solar, flow, domain, width, height }: EnergyChartProps) {
  const { start, end, step, min, max } = domain;
  const stroke = 3;
  const toX = linear(start, end, 0, width);
  const x = (t: number) => toX(t + step / 2);
  const y = yScale(min, max, height, stroke);
  const xy = (t: number, w: number): [number, number] => [x(t), y(w)];
  const zero = y(0);
  const flowW = flow.map((p) => ({ t: p.t, v: p.v == null ? null : p.v * 1000 }));
  return (
    <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`}>
      {runs(solar).map((r, i) => {
        const first = r[0]!;
        const last = r[r.length - 1]!;
        return (
          <polygon
            key={`s${i}`}
            // Edges half a step out, so a single bucket still has width.
            points={svgPoints([xy(first.t - step / 2, 0), xy(first.t - step / 2, first.v), ...r.map((p) => xy(p.t, p.v)), xy(last.t + step / 2, last.v), xy(last.t + step / 2, 0)])}
            fill="#aaa"
          />
        );
      })}
      <line x1={0} y1={zero} x2={width} y2={zero} stroke="#555" strokeWidth={1} />
      {runs(flowW).map((r, i) =>
        r.length === 1 ? (
          <circle key={`f${i}`} cx={x(r[0]!.t)} cy={y(r[0]!.v)} r={stroke} fill="#000" />
        ) : (
          <polyline
            key={`f${i}`}
            points={svgPoints(r.map((p) => xy(p.t, p.v)))}
            fill="none"
            stroke="#000"
            strokeWidth={stroke}
            strokeLinejoin="round"
            strokeLinecap="round"
          />
        ),
      )}
    </svg>
  );
}
