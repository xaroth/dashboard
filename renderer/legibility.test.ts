import { Renderer } from "@takumi-rs/core";
import { expect, test } from "bun:test";
import { createElement } from "react";
import { measure, render } from "takumi-js";
import { reducedLevel, score, textRuns } from "./legibility";

test("reducedLevel matches reduce in render/image.go", () => {
  // The cases of render/image_test.go's TestFinish, as darkness levels.
  const cases: [number[], number][] = [
    [[0, 0, 0, 255], 3],
    [[80, 80, 80, 255], 2],
    [[180, 180, 180, 255], 1],
    [[255, 255, 255, 255], 0],
    [[0, 0, 0, 0], 0],
    [[0, 0, 0, 64], 1],
    [[40, 40, 40, 255], 3],
    [[200, 200, 200, 255], 1],
    [[128, 128, 128, 255], 2],
    [[220, 220, 220, 255], 1],
  ];
  for (const [px, want] of cases) expect([px, reducedLevel(new Uint8Array(px), 0)]).toEqual([px, want]);
});

const renderer = new Renderer();

async function scores(style: Record<string, string | number>, text: string, background = "#fff") {
  const el = createElement("div", { style: { display: "flex", padding: 10, backgroundColor: background } }, createElement("span", { style }, text));
  const opts = { renderer, width: 300, height: 100 };
  const data = await render(el, { ...opts, format: "raw" });
  const runs = textRuns(await measure(el, opts));
  expect(runs.map((r) => r.text)).toEqual([text]);
  return score({ data: new Uint8Array(data), width: 300, height: 100 }, runs[0]!);
}

test("big black text keeps its strokes", async () => {
  expect(await scores({ fontSize: 40, fontWeight: 700 }, "Afname")).toBeGreaterThan(0.9);
});

test("tiny gray text loses its strokes", async () => {
  expect(await scores({ fontSize: 9, fontWeight: 300, color: "#555" }, "afname")).toBeLessThan(0.3);
});

test("white text on black is scored against the black", async () => {
  expect(await scores({ fontSize: 40, fontWeight: 700, color: "#fff" }, "Afname", "#000")).toBeGreaterThan(0.6);
});

test("a run without ink has no score", async () => {
  expect(await scores({ fontSize: 20, color: "#fff" }, "Afname")).toBeNull();
});
