import type { ReactNode } from "react";
import type { WeatherIconName } from "../lib/weather";

/** Line and fill color, and the background the icon cuts out of. */
type Ink = { fg: string; bg: string };

const line = (ink: Ink) => ({ stroke: ink.fg, strokeWidth: 3, strokeLinecap: "round", strokeLinejoin: "round" }) as const;

function Sun({ cx, cy, r, ray, ink }: { cx: number; cy: number; r: number; ray: number; ink: Ink }) {
  const rays = Array.from({ length: 8 }, (_, i) => {
    const a = (i * Math.PI) / 4;
    const [c, s] = [Math.cos(a), Math.sin(a)];
    return `M${cx + c * (r + 4)} ${cy + s * (r + 4)}L${cx + c * (r + 4 + ray)} ${cy + s * (r + 4 + ray)}`;
  });
  return (
    <g>
      <circle cx={cx} cy={cy} r={r} fill={ink.fg} />
      <path d={rays.join("")} fill="none" {...line(ink)} strokeWidth={2.5} />
    </g>
  );
}

const moon = "M26 6a14 14 0 1 0 8 22A12 12 0 0 1 26 6z";

function icon(name: WeatherIconName, ink: Ink): ReactNode {
  const cloud = (y = 0) => (
    <path d={`M10 ${31 + y}h20a7.5 7.5 0 0 0 0-15 10 10 0 0 0-19 3.5A6 6 0 0 0 10 ${31 + y}z`} fill="#aaa" {...line(ink)} />
  );
  const smallCloud = <path d="M12 34h20a7 7 0 0 0 0-14 9 9 0 0 0-17 3 5.5 5.5 0 0 0-3 11z" fill={ink.bg} {...line(ink)} />;
  const below = (d: string) => <path d={d} fill="none" {...line(ink)} />;
  switch (name) {
    case "sun":
      return <Sun cx={20} cy={20} r={7} ray={5} ink={ink} />;
    case "moon":
      return <path d={moon} fill={ink.fg} />;
    case "partly":
      return (
        <g>
          <Sun cx={14} cy={14} r={5.5} ray={3} ink={ink} />
          {smallCloud}
        </g>
      );
    case "partlyNight":
      return (
        <g>
          <path d={moon} fill={ink.fg} transform="translate(-6 -4) scale(0.75)" />
          {smallCloud}
        </g>
      );
    case "cloud":
      return cloud();
    case "fog":
      return (
        <g>
          {cloud(-6)}
          {below("M6 31h28M10 37h20")}
        </g>
      );
    case "rain":
      return (
        <g>
          {cloud(-6)}
          {below("M14 30l-2 6M21 30l-2 6M28 30l-2 6")}
        </g>
      );
    case "snow":
      return (
        <g>
          {cloud(-6)}
          <g fill={ink.fg}>
            <circle cx={13} cy={32} r={2.2} />
            <circle cx={20} cy={36} r={2.2} />
            <circle cx={27} cy={32} r={2.2} />
          </g>
        </g>
      );
    case "thunder":
      return (
        <g>
          {cloud(-6)}
          <path d="M22 24l-6 9h5l-3 7 9-10h-5l3-6z" fill={ink.fg} />
        </g>
      );
  }
}

/** A weather icon in black and grays, drawn in a 40x40 box; inverted draws it in white for a black background. */
export function WeatherIcon({ name, size, inverted }: { name: WeatherIconName; size: number; inverted?: boolean }) {
  return (
    <svg width={size} height={size} viewBox="0 0 40 40">
      {icon(name, inverted ? { fg: "#fff", bg: "#000" } : { fg: "#000", bg: "#fff" })}
    </svg>
  );
}
