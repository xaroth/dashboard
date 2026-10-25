import { paddedRange, svgPoints, yScale } from "../lib/chart";

export type LineChartProps = {
  values: number[];
  /** Bars behind the line, 0-100, e.g. precipitation probability; null draws nothing. */
  bars?: (number | null)[];
  width: number;
  height: number;
};

/** A line over optional bars, as inline SVG; the value range is padded so the line never touches the edges. */
export function LineChart({ values, bars, width, height }: LineChartProps) {
  if (values.length < 2) return <div style={{ width, height }} />;
  const [lo, hi] = paddedRange(values, 0.15, 0.5);
  const stroke = 4;
  // Each value gets a column; points sit at column centers, over their bars.
  const barWidth = width / values.length;
  const y = yScale(lo, hi, height, stroke);
  const points = svgPoints(values.map((v, i) => [(i + 0.5) * barWidth, y(v)]));
  const barHeight = (b: number) => (Math.min(b, 100) / 100) * height;
  return (
    <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`}>
      {bars?.map((b, i) =>
        b == null || b <= 0 ? null : (
          <rect key={i} x={i * barWidth} y={height - barHeight(b)} width={barWidth - 1} height={barHeight(b)} fill="#aaa" />
        ),
      )}
      <polyline points={points} fill="none" stroke="#000" strokeWidth={stroke} strokeLinejoin="round" strokeLinecap="round" />
    </svg>
  );
}
