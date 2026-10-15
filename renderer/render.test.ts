import { Renderer } from "@takumi-rs/core";
import { expect, test } from "bun:test";
import { join } from "node:path";
import { bundlePages } from "./bundle";
import { renderPage } from "./render";

const root = join(import.meta.dir, "..");

test("bundles and renders the hello page", async () => {
  const bundle = await bundlePages(join(root, "pages"));
  expect(bundle.hash).toMatch(/^[0-9a-f]{12}$/);
  const hello = bundle.pages.get("hello")!;
  expect(hello.css).toContain("font-size");

  const renderer = new Renderer();
  const png = await renderPage(renderer, hello, { width: 800, height: 480, portrait: false }, {
    data: {},
    device: { name: "test", model: "reterminal_e1001", rotation: 0 },
    system: { staleModules: [] },
    now: "2026-10-16T05:30:00Z",
    timezone: "Europe/Amsterdam",
  });
  expect([...png.slice(1, 4)]).toEqual([80, 78, 71]);
  const view = new DataView(png.buffer, png.byteOffset);
  expect([view.getUint32(16), view.getUint32(20)]).toEqual([800, 480]);
});
