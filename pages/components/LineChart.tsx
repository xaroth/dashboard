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
  const min = Math.min(...values);
  const max = Math.max(...values);
  const pad = Math.max((max - min) * 0.15, 0.5);
  const lo = min - pad;
  const hi = max + pad;
  const stroke = 4;
  // Each value gets a column; points sit at column centers, over their bars.
  const barWidth = width / values.length;
  const x = (i: number) => (i + 0.5) * barWidth;
  // Inset by half the stroke so the line is not clipped at the top and bottom.
  const y = (v: number) => stroke / 2 + (1 - (v - lo) / (hi - lo)) * (height - stroke);
  const points = values.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(" ");
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
