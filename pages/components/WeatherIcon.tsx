import type { ReactNode } from "react";
import type { WeatherIconName } from "../lib/weather";

const S = { stroke: "#000", strokeWidth: 3, strokeLinecap: "round", strokeLinejoin: "round" } as const;

function Sun({ cx, cy, r, ray }: { cx: number; cy: number; r: number; ray: number }) {
  const rays = Array.from({ length: 8 }, (_, i) => {
    const a = (i * Math.PI) / 4;
    const [c, s] = [Math.cos(a), Math.sin(a)];
    return `M${cx + c * (r + 4)} ${cy + s * (r + 4)}L${cx + c * (r + 4 + ray)} ${cy + s * (r + 4 + ray)}`;
  });
  return (
    <g>
      <circle cx={cx} cy={cy} r={r} fill="#000" />
      <path d={rays.join("")} fill="none" {...S} strokeWidth={2.5} />
    </g>
  );
}

const moon = "M26 6a14 14 0 1 0 8 22A12 12 0 0 1 26 6z";
const cloud = (fill: string, y = 0) => (
  <path d={`M10 ${31 + y}h20a7.5 7.5 0 0 0 0-15 10 10 0 0 0-19 3.5A6 6 0 0 0 10 ${31 + y}z`} fill={fill} {...S} />
);
const smallCloud = <path d="M12 34h20a7 7 0 0 0 0-14 9 9 0 0 0-17 3 5.5 5.5 0 0 0-3 11z" fill="#fff" {...S} />;
const below = (d: string) => <path d={d} fill="none" {...S} />;

const icons: Record<WeatherIconName, ReactNode> = {
  sun: <Sun cx={20} cy={20} r={7} ray={5} />,
  moon: <path d={moon} fill="#000" />,
  partly: (
    <g>
      <Sun cx={14} cy={14} r={5.5} ray={3} />
      {smallCloud}
    </g>
  ),
  partlyNight: (
    <g>
      <path d={moon} fill="#000" transform="translate(-6 -4) scale(0.75)" />
      {smallCloud}
    </g>
  ),
  cloud: cloud("#aaa"),
  fog: (
    <g>
      {cloud("#aaa", -6)}
      {below("M6 31h28M10 37h20")}
    </g>
  ),
  rain: (
    <g>
      {cloud("#aaa", -6)}
      {below("M14 30l-2 6M21 30l-2 6M28 30l-2 6")}
    </g>
  ),
  snow: (
    <g>
      {cloud("#aaa", -6)}
      <g fill="#000">
        <circle cx={13} cy={32} r={2.2} />
        <circle cx={20} cy={36} r={2.2} />
        <circle cx={27} cy={32} r={2.2} />
      </g>
    </g>
  ),
  thunder: (
    <g>
      {cloud("#aaa", -6)}
      <path d="M22 24l-6 9h5l-3 7 9-10h-5l3-6z" fill="#000" />
    </g>
  ),
};

/** A weather icon in black and grays, drawn in a 40x40 box. */
export function WeatherIcon({ name, size }: { name: WeatherIconName; size: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 40 40">
      {icons[name]}
    </svg>
  );
}
