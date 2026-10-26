import type { MeasuredNode } from "@takumi-rs/core";

/** An RGBA image with straight alpha, as takumi renders with format "raw". */
export type Image = { data: Uint8Array; width: number; height: number };

/** A text run's box in device px. */
export type TextRun = { text: string; x: number; y: number; width: number; height: number };

/** Every non-blank text run in a measured tree, in device px. */
export function textRuns(node: MeasuredNode): TextRun[] {
  const [, , , , nx, ny] = node.transform;
  const runs = node.runs
    .filter((r) => r.text.trim() !== "")
    .map((r) => ({ text: r.text.trim(), x: nx + r.x, y: ny + r.y, width: r.width, height: r.height }));
  return runs.concat(node.children.flatMap(textRuns));
}

// Go's 16-bit luminance of a straight-alpha pixel on white, as render/image.go's reduce computes it.
function luminance(data: Uint8Array, i: number): number {
  const a = data[i + 3]! * 0x101;
  const white = 0xffff - a;
  const [r, g, b] = [0, 1, 2].map((c) => Math.floor((data[i + c]! * 0x101 * a) / 0xffff) + white) as [number, number, number];
  return Math.floor((299 * r + 587 * g + 114 * b) / 1000);
}

/**
 * The darkness level (0 paper to 3 black) the panel shows for a pixel. Keep
 * in sync with reduce in render/image.go, which gives the lightness level.
 */
export function reducedLevel(data: Uint8Array, i: number): number {
  return 3 - Math.floor((luminance(data, i) * 3 + Math.floor(0xffff / 4)) / 0xffff);
}

function darkness(data: Uint8Array, i: number): number {
  return 1 - luminance(data, i) / 0xffff;
}

/**
 * The share of a run's stroke pixels that keep their gray level on the panel,
 * or null for a run without ink. The background is the box's most common
 * gray and the ink its gray furthest from it, darker or lighter. Strokes are
 * the pixels at least halfway from background to ink; they should reduce to
 * the ink's nearest level. Thin strokes antialias into pixels closer to the
 * background, which the reduction can turn a level or more too light.
 */
export function score(img: Image, run: TextRun): number | null {
  // Only pixels wholly inside the box: the partial ones at its edges are often
  // a neighbor's border. The box spans the font's ascent to descent; glyphs of
  // the line above overflow into its top, above where this run's glyphs reach.
  const x0 = Math.max(0, Math.ceil(run.x));
  const y0 = Math.max(0, Math.ceil(run.y + run.height * 0.15));
  const x1 = Math.min(img.width, Math.floor(run.x + run.width));
  const y1 = Math.min(img.height, Math.floor(run.y + run.height));
  const d: number[] = [];
  for (let y = y0; y < y1; y++) {
    for (let x = x0; x < x1; x++) d.push(darkness(img.data, (y * img.width + x) * 4));
  }
  if (d.length === 0) return null;
  const counts = new Map<number, number>();
  let max = 0;
  let min = 1;
  for (const v of d) {
    const k = Math.round(v * 255);
    counts.set(k, (counts.get(k) ?? 0) + 1);
    max = Math.max(max, v);
    min = Math.min(min, v);
  }
  const bg = [...counts].reduce((a, b) => (b[1] > a[1] ? b : a))[0] / 255;
  const dark = max - bg >= bg - min;
  const ink = dark ? max : min;
  if (Math.abs(ink - bg) < 0.1) return null;
  const target = Math.round(ink * 3);
  let core = 0;
  let kept = 0;
  let i = 0;
  for (let y = y0; y < y1; y++) {
    for (let x = x0; x < x1; x++) {
      const v = d[i++]!;
      if (Math.abs(v - bg) < Math.abs(ink - bg) / 2) continue;
      core++;
      const level = reducedLevel(img.data, (y * img.width + x) * 4);
      if (dark ? level >= target : level <= target) kept++;
    }
  }
  return kept / core;
}
