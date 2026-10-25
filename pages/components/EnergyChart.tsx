import { runs, type SeriesPoint } from "../lib/energy";

export type EnergyChartProps = {
  /** Solar power in W, drawn as a gray area. */
  solar: SeriesPoint[];
  /** Net flow in kW, + taken, - returned, drawn as a line. */
  flow: SeriesPoint[];
  /** Time range the x axis spans and the bucket size, in ms; values sit at bucket centers. */
  start: number;
  end: number;
  step: number;
  /** Value range in W. */
  min: number;
  max: number;
  width: number;
  height: number;
};

/** Solar production and net flow over time, as inline SVG with a zero line. */
export function EnergyChart({ solar, flow, start, end, step, min, max, width, height }: EnergyChartProps) {
  const stroke = 3;
  const x = (t: number) => ((t + step / 2 - start) / (end - start)) * width;
  const y = (w: number) => stroke / 2 + (1 - (w - min) / (max - min)) * (height - stroke);
  const xy = (t: number, w: number) => `${x(t).toFixed(1)},${y(w).toFixed(1)}`;
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
            points={[xy(first.t, 0), ...r.map((p) => xy(p.t, p.v)), xy(last.t, 0)].join(" ")}
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
            points={r.map((p) => xy(p.t, p.v)).join(" ")}
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
