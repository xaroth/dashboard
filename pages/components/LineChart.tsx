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
  const step = width / (values.length - 1);
  const y = (v: number) => height - ((v - lo) / (hi - lo)) * height;
  const points = values.map((v, i) => `${(i * step).toFixed(1)},${y(v).toFixed(1)}`).join(" ");
  const barWidth = width / values.length;
  return (
    <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`}>
      {bars?.map((b, i) =>
        b == null || b <= 0 ? null : (
          <rect key={i} x={i * barWidth} y={height - (b / 100) * height} width={barWidth - 1} height={(b / 100) * height} fill="#aaa" />
        ),
      )}
      <polyline points={points} fill="none" stroke="#000" strokeWidth={4} strokeLinejoin="round" strokeLinecap="round" />
    </svg>
  );
}
