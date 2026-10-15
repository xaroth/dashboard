import { expect, test } from "bun:test";
import { join } from "node:path";
import { encodeFrame, FrameReader } from "./frame";

const root = join(import.meta.dir, "..");

test("process answers describe and render over frames", async () => {
  const proc = Bun.spawn(["bun", join(root, "renderer/main.ts"), "--pages", join(root, "pages"), "--fonts", join(root, "fonts")], {
    stdin: "pipe",
    stdout: "pipe",
    stderr: "pipe",
  });
  const reader = new FrameReader(proc.stdout);

  proc.stdin.write(encodeFrame({ id: 1, op: "describe" }));
  proc.stdin.flush();
  const desc = await reader.next();
  expect(desc!.header.id).toBe(1);
  expect(desc!.header.pages).toContainEqual({ name: "hello", data: {} });

  proc.stdin.write(encodeFrame({ id: 2, op: "render", page: "nope", viewport: { width: 10, height: 10, portrait: false } }));
  proc.stdin.flush();
  expect((await reader.next())!.header).toMatchObject({ id: 2, error: "unknown page nope" });

  proc.stdin.write(encodeFrame({
    id: 3,
    op: "render",
    page: "hello",
    viewport: { width: 800, height: 480, portrait: false },
    props: { data: {}, device: { name: "t", model: "m", rotation: 0 }, system: { staleModules: [] }, now: "2026-10-16T05:30:00Z", timezone: "UTC" },
  }));
  proc.stdin.flush();
  const png = await reader.next();
  expect(png!.header.id).toBe(3);
  expect(png!.body!.length).toBe(png!.header.png as number);

  proc.stdin.end();
  expect(await proc.exited).toBe(0);
}, 20000);
