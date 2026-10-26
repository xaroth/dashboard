// Renders every page with the sample data in fixtures.ts, as HTML or PNG,
// without the service. Usage: bun renderer/preview.ts [--pages pages] [--port 8090] [--watch]
import { Renderer } from "@takumi-rs/core";
import { join } from "node:path";
import { parseArgs } from "node:util";
import { bundlePages, type Page } from "./bundle";
import { renderHTML, renderPage } from "./render";
import { rebuildOnChange } from "./watch";

const { values: args } = parseArgs({
  options: {
    pages: { type: "string", default: "pages" },
    port: { type: "string", default: "8090" },
    watch: { type: "boolean", default: false },
  },
});

const fixturesFile = join(import.meta.dir, "fixtures.ts");
type Fixtures = typeof import("./fixtures");

const renderer = new Renderer();
let pages: Map<string, Page> = await bundlePages(args.pages!);
let fixtures: Fixtures = await import(fixturesFile);
let version = 0;
let buildError = "";

if (args.watch) {
  rebuildOnChange([args.pages!, fixturesFile], async () => {
    try {
      // A fresh path per build, so the edited fixtures are not served from the module cache.
      [pages, fixtures] = await Promise.all([bundlePages(args.pages!), import(`${fixturesFile}?v=${Date.now()}`)]);
      buildError = "";
      console.error(`preview: rebuilt ${pages.size} pages`);
    } catch (e) {
      buildError = String(e);
      console.error(`preview: rebuild failed, keeping previous bundle: ${e}`);
    }
    version++;
  });
}

// Reloads the top window once a rebuild finishes; gallery frames reload with it.
const reloadScript = `<script>
if (window === top) {
  let seen;
  setInterval(async () => {
    const v = await fetch("/version").then((r) => r.text(), () => seen);
    if (seen !== undefined && v !== seen) location.reload();
    seen = v;
  }, 500);
}
</script>`;

const escape = (s: string) => s.replace(/[&<>"]/g, (c) => `&#${c.charCodeAt(0)};`);

function gallery(mode: "html" | "png", portrait: boolean, only: string | null): string {
  const viewport = fixtures.viewports.find((v) => v.portrait === portrait)!;
  const query = portrait ? "?portrait=1" : "";
  const link = (m: string, p: boolean, page = only) => {
    const q = new URLSearchParams({ mode: m, ...(p ? { portrait: "1" } : {}), ...(page ? { page } : {}) });
    return `/?${q}`;
  };
  const rows = [...pages.values()]
    .filter((p) => !only || p.name === only)
    .map((page) => {
      const tiles = Object.keys(fixtures.variants).map((variant) => {
        const src = `/page/${page.name}/${variant}.${mode}${query}`;
        const view =
          mode === "html"
            ? `<iframe src="${src}" width="${viewport.width}" height="${viewport.height}"></iframe>`
            : `<img src="${src}" width="${viewport.width}" height="${viewport.height}">`;
        return `<figure><figcaption><a href="${src}" target="_blank">${variant}</a></figcaption>${view}</figure>`;
      });
      return `<section><h2><a href="${link(mode, portrait, page.name)}">${page.name}</a></h2><div class="tiles">${tiles.join("")}</div></section>`;
    });
  const error = buildError ? `<pre class="error">${escape(buildError)}</pre>` : "";
  return `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>Page preview</title>
<style>
body { font-family: system-ui, sans-serif; margin: 16px; background: #eee; }
nav a { margin-right: 12px; }
.tiles { display: flex; flex-wrap: wrap; gap: 16px; }
figure { margin: 0; }
iframe, img { display: block; border: 1px solid #999; background: #fff; }
.error { background: #fdd; padding: 8px; white-space: pre-wrap; }
</style>
</head>
<body>
<nav>
<a href="${link("html", portrait)}">HTML</a><a href="${link("png", portrait)}">PNG</a> |
<a href="${link(mode, false)}">landscape</a><a href="${link(mode, true)}">portrait</a> |
<a href="${link(mode, portrait, null)}">all pages</a>
</nav>
${error}
${rows.join("\n")}
${args.watch ? reloadScript : ""}
</body>
</html>
`;
}

async function renderOne(url: URL): Promise<Response> {
  const m = url.pathname.match(/^\/page\/(.+)\/([^/]+)\.(html|png)$/);
  if (!m) return new Response("not found", { status: 404 });
  const [, name, variant, format] = m;
  const page = pages.get(name!);
  const result = fixtures.variants[variant!];
  if (!page || !result) return new Response("not found", { status: 404 });
  const portrait = url.searchParams.has("portrait");
  const viewport = fixtures.viewports.find((v) => v.portrait === portrait)!;
  const props = fixtures.props(page, result, viewport);
  try {
    if (format === "png") return new Response((await renderPage(renderer, page, props)) as Uint8Array<ArrayBuffer>, { headers: { "Content-Type": "image/png" } });
    const html = renderHTML(page, props).replace("</body>", `${args.watch ? reloadScript : ""}</body>`);
    return new Response(html, { headers: { "Content-Type": "text/html; charset=utf-8" } });
  } catch (e) {
    const msg = e instanceof Error ? (e.stack ?? e.message) : String(e);
    return new Response(`<pre>${escape(msg)}</pre>`, { status: 500, headers: { "Content-Type": "text/html; charset=utf-8" } });
  }
}

const server = Bun.serve({
  hostname: "127.0.0.1",
  port: Number(args.port),
  fetch(req) {
    const url = new URL(req.url);
    if (url.pathname === "/") {
      const mode = url.searchParams.get("mode") === "png" ? "png" : "html";
      const html = gallery(mode, url.searchParams.has("portrait"), url.searchParams.get("page"));
      return new Response(html, { headers: { "Content-Type": "text/html; charset=utf-8" } });
    }
    if (url.pathname === "/version") return new Response(String(version));
    if (url.pathname === "/favicon.ico") return new Response(null, { status: 204 });
    return renderOne(url);
  },
});
console.error(`preview: ${pages.size} pages at ${server.url}`);
