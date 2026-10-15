// Long-running render process. Reads request frames on stdin, writes response
// frames on stdout. Usage: bun renderer/main.ts --pages pages [--watch]
import { Renderer } from "@takumi-rs/core";
import { watch } from "node:fs";
import { parseArgs } from "node:util";
import { bundlePages, type Page } from "./bundle";
import { encodeFrame, FrameReader } from "./frame";
import { renderPage } from "./render";

// stdout carries frames only; frames go through Bun.stdout, which these do not touch.
console.log = console.info = console.debug = console.error;
process.stdout.write = process.stderr.write.bind(process.stderr) as typeof process.stdout.write;

const { values: args } = parseArgs({
  options: {
    pages: { type: "string", default: "pages" },
    watch: { type: "boolean", default: false },
  },
});

const renderer = new Renderer();
let pages: Map<string, Page> = await bundlePages(args.pages!);
console.error(`renderer: bundled ${pages.size} pages`);

if (args.watch) {
  let timer: Timer | undefined;
  let building = false;
  let again = false;
  const rebuild = async () => {
    // One build at a time, so an older build never replaces a newer one.
    if (building) return void (again = true);
    building = true;
    try {
      pages = await bundlePages(args.pages!);
      console.error(`renderer: rebuilt ${pages.size} pages`);
    } catch (e) {
      console.error(`renderer: rebuild failed, keeping previous bundle: ${e}`);
    } finally {
      building = false;
      if (again) {
        again = false;
        rebuild();
      }
    }
  };
  watch(args.pages!, { recursive: true }, () => {
    clearTimeout(timer);
    timer = setTimeout(rebuild, 100);
  });
}

const out = Bun.stdout.writer();
function write(header: Record<string, unknown>, body?: Uint8Array) {
  out.write(encodeFrame(header, body));
  out.flush();
}

async function handle(req: Record<string, any>): Promise<void> {
  const id = req.id;
  switch (req.op) {
    case "describe":
      return write({
        id,
        pages: [...pages.values()].map((p) => ({ name: p.name, data: p.data })),
      });
    case "render": {
      const page = pages.get(req.page);
      if (!page) return write({ id, error: `unknown page ${req.page}` });
      const png = await renderPage(renderer, page, req.props);
      return write({ id }, png);
    }
    default:
      return write({ id, error: `unknown op ${req.op}` });
  }
}

const reader = new FrameReader(Bun.stdin.stream());
for (let frame; (frame = await reader.next()); ) {
  try {
    await handle(frame.header);
  } catch (e) {
    write({ id: frame.header.id, error: String(e instanceof Error ? e.message : e) });
  }
}
// stdin closed: the Go side is gone. Exit even if the page watcher is running.
process.exit(0);
