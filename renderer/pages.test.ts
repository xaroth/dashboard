import { Renderer } from "@takumi-rs/core";
import { describe, expect, test } from "bun:test";
import { Glob } from "bun";
import { join } from "node:path";
import { createElement } from "react";
import { measure, render } from "takumi-js";
import { bundlePages } from "./bundle";
import { props, variants, viewports } from "./fixtures";
import { score, textRuns } from "./legibility";
import { layout, renderPage } from "./render";

const pagesDir = join(import.meta.dir, "../pages");

const pages = await bundlePages(pagesDir);
const renderer = new Renderer();

describe("every page renders", () => {
  for (const page of pages.values()) {
    for (const [variant, result] of Object.entries(variants)) {
      for (const viewport of viewports) {
        test(`${page.name} ${variant} ${viewport.width}x${viewport.height}`, async () => {
          const png = await renderPage(renderer, page, props(page, result, viewport));
          expect([...png.slice(1, 4)]).toEqual([80, 78, 71]);
          const view = new DataView(png.buffer, png.byteOffset);
          expect([view.getUint32(16), view.getUint32(20)]).toEqual([viewport.width, viewport.height]);
        });
      }
    }
  }
});

// Lowest scores of the current pages are ~0.36 (e1001/home's "!" unscaled on the E1003) and ~0.42.
const legible = 0.35;

const panels = [
  { model: "reterminal_e1001", width: 800, height: 480 },
  { model: "reterminal_e1003", width: 1872, height: 1404 },
].flatMap((p) => [
  { ...p, portrait: false },
  { ...p, width: p.height, height: p.width, portrait: true },
]);

describe("text stays legible after the reduction to 4 grays", () => {
  for (const page of pages.values()) {
    for (const { model, ...panel } of panels) {
      test(`${page.name} ${model} ${panel.width}x${panel.height}`, async () => {
        const p = props(page, variants.populated!, panel);
        p.device.model = model;
        const { viewport, scale } = layout(page, panel);
        const element = createElement(page.component, { ...p, viewport });
        const options = { renderer, width: panel.width, height: panel.height, devicePixelRatio: scale, css: page.css };
        const [data, tree] = await Promise.all([render(element, { ...options, format: "raw" }), measure(element, options)]);
        const img = { data: new Uint8Array(data), width: panel.width, height: panel.height };
        const failing = textRuns(tree)
          .map((run) => ({ text: run.text, score: score(img, run) }))
          .filter((run) => run.score !== null && run.score < legible)
          .map((run) => `${JSON.stringify(run.text)} ${run.score!.toFixed(2)}`);
        expect(failing).toEqual([]);
      });
    }
  }
});

test("every page is bundled", () => {
  const files = ["layouts/*/*.tsx", "shared/*.tsx"].flatMap((p) => [...new Glob(p).scanSync({ cwd: pagesDir })]);
  expect(pages.size).toBe(files.length);
});

// CSS modules are typed loosely (css.d.ts); a misspelled class would be undefined and silently unstyled.
test("every class a component uses is in its CSS module", async () => {
  const missing: string[] = [];
  for (const file of new Glob("**/*.tsx").scanSync({ cwd: pagesDir })) {
    const src = await Bun.file(join(pagesDir, file)).text();
    const imp = src.match(/import (\w+) from "(\.[^"]+\.module\.css)"/);
    if (!imp) continue;
    const [, ident, cssPath] = imp;
    const css = await Bun.file(join(pagesDir, file, "..", cssPath!)).text();
    const defined = new Set([...css.matchAll(/\.([A-Za-z_][\w-]*)/g)].map((m) => m[1]));
    for (const [, cls] of src.matchAll(new RegExp(`\\b${ident}\\.(\\w+)`, "g"))) {
      if (!defined.has(cls)) missing.push(`${file}: ${cls}`);
    }
  }
  expect(missing).toEqual([]);
});
