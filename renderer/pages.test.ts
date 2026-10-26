import { Renderer } from "@takumi-rs/core";
import { describe, expect, test } from "bun:test";
import { Glob } from "bun";
import { join } from "node:path";
import { bundlePages } from "./bundle";
import { props, variants, viewports } from "./fixtures";
import { renderPage } from "./render";

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
