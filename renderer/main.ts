// Long-running render process. Reads request frames on stdin, writes response
// frames on stdout. Usage: bun renderer/main.ts --pages pages --fonts fonts [--watch]
import { watch } from "node:fs";
import { parseArgs } from "node:util";
import { bundlePages, type Bundle } from "./bundle";
import { encodeFrame, FrameReader } from "./frame";
import { loadRenderer, renderPage } from "./render";

// stdout carries frames only.
console.log = console.info = console.debug = console.error;

const { values: args } = parseArgs({
  options: {
    pages: { type: "string", default: "pages" },
    fonts: { type: "string", default: "fonts" },
    watch: { type: "boolean", default: false },
  },
});

const renderer = await loadRenderer(args.fonts!);
let bundle: Bundle = await bundlePages(args.pages!);
console.error(`renderer: bundled ${bundle.pages.size} pages, hash ${bundle.hash}`);

if (args.watch) {
  let timer: Timer | undefined;
  watch(args.pages!, { recursive: true }, () => {
    clearTimeout(timer);
    timer = setTimeout(async () => {
      try {
        bundle = await bundlePages(args.pages!);
        console.error(`renderer: rebuilt, hash ${bundle.hash}`);
      } catch (e) {
        console.error(`renderer: rebuild failed, keeping previous bundle: ${e}`);
      }
    }, 100);
  });
}

const out = Bun.stdout.writer();
function send(header: Record<string, unknown>, body?: Uint8Array) {
  out.write(encodeFrame({ ...header, hash: bundle.hash }, body));
  out.flush();
}

async function handle(req: Record<string, any>): Promise<void> {
  const id = req.id;
  switch (req.op) {
    case "describe":
      return send({
        id,
        pages: [...bundle.pages.values()].map((p) => ({ name: p.name, data: p.data })),
      });
    case "render": {
      const page = bundle.pages.get(req.page);
      if (!page) return send({ id, error: `unknown page ${req.page}` });
      const png = await renderPage(renderer, page, req.viewport, req.props ?? {});
      return send({ id }, png);
    }
    default:
      return send({ id, error: `unknown op ${req.op}` });
  }
}

const reader = new FrameReader(Bun.stdin.stream());
for (let frame; (frame = await reader.next()); ) {
  try {
    await handle(frame.header);
  } catch (e) {
    send({ id: frame.header.id, error: String(e instanceof Error ? e.message : e) });
  }
}
