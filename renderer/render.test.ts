import { Renderer } from "@takumi-rs/core";
import { expect, test } from "bun:test";
import { join } from "node:path";
import { bundlePages } from "./bundle";
import { layout, renderHTML, renderPage } from "./render";

const root = join(import.meta.dir, "..");

test("bundles and renders the hello page", async () => {
  const pages = await bundlePages(join(root, "pages"));
  const hello = pages.get("hello")!;
  expect(hello.css).toContain("font-size");

  const renderer = new Renderer();
  const png = await renderPage(renderer, hello, {
    data: {},
    device: { name: "test", model: "reterminal_e1001", rotation: 0 },
    viewport: { width: 800, height: 480, portrait: false },
    system: { staleModules: [] },
    now: "2026-10-16T05:30:00Z",
    timezone: "Europe/Amsterdam",
  });
  expect([...png.slice(1, 4)]).toEqual([80, 78, 71]);
  const view = new DataView(png.buffer, png.byteOffset);
  expect([view.getUint32(16), view.getUint32(20)]).toEqual([800, 480]);
});

test("pages with the same name in two layouts and shared stay apart", async () => {
  const pages = await bundlePages(join(import.meta.dir, "testdata/same-name"));
  expect([...pages.keys()]).toEqual(["large/home", "small/home", "home"]);
  expect(pages.get("small/home")!.css).toContain("1px");
  expect(pages.get("large/home")!.css).toContain("2px");
  expect(pages.get("home")!.css).toContain("3px");
  expect([...pages.values()].map((p) => p.shared)).toEqual([false, false, true]);
});

test("shared pages lay out at 800x480 and scale to the panel", async () => {
  const pages = await bundlePages(join(import.meta.dir, "testdata/same-name"));
  const e1003 = { width: 1872, height: 1404, portrait: false };
  expect(layout(pages.get("home")!, e1003)).toEqual({ viewport: { width: 800, height: 600, portrait: false }, scale: 2.34 });
  expect(layout(pages.get("home")!, { width: 1404, height: 1872, portrait: true })).toEqual({
    viewport: { width: 600, height: 800, portrait: true },
    scale: 2.34,
  });
  expect(layout(pages.get("home")!, { width: 800, height: 480, portrait: false }).scale).toBe(1);
  expect(layout(pages.get("large/home")!, e1003)).toEqual({ viewport: e1003, scale: 1 });

  const png = await renderPage(new Renderer(), pages.get("home")!, {
    data: {},
    device: { name: "test", model: "reterminal_e1003", rotation: 0 },
    viewport: e1003,
    system: { staleModules: [] },
    now: "2026-10-16T05:30:00Z",
    timezone: "Europe/Amsterdam",
  });
  const view = new DataView(png.buffer, png.byteOffset);
  expect([view.getUint32(16), view.getUint32(20)]).toEqual([1872, 1404]);
});

test("renders a page as an HTML document of the viewport's size", async () => {
  const pages = await bundlePages(join(root, "pages"));
  const hello = pages.get("hello")!;
  const html = renderHTML(hello, {
    data: {},
    device: { name: "test", model: "reterminal_e1001", rotation: 90 },
    viewport: { width: 480, height: 800, portrait: true },
    system: { staleModules: [] },
    now: "2026-10-16T05:30:00Z",
    timezone: "Europe/Amsterdam",
  });
  expect(html).toStartWith("<!doctype html>");
  expect(html).toContain("width: 480px; height: 800px;");
  expect(html).toContain(hello.css);
  expect(html).toContain("test (reterminal_e1001)");
  // Takumi's font, inlined: the document needs no network.
  expect(html).toContain("src: url(data:font/woff2;base64,");
  expect(html).not.toMatch(/https?:\/\//);
});
