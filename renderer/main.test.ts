import { expect, test } from "bun:test";
import { join } from "node:path";
import { encodeFrame, FrameReader } from "./frame";

const root = join(import.meta.dir, "..");

test("process answers describe and render over frames", async () => {
  const proc = Bun.spawn(["bun", join(root, "renderer/main.ts"), "--pages", join(root, "pages")], {
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

  const props = {
    data: {},
    device: { name: "t", model: "m", rotation: 0 },
    viewport: { width: 800, height: 480, portrait: false },
    system: { staleModules: [] },
    now: "2026-10-16T05:30:00Z",
    timezone: "UTC",
  };
  proc.stdin.write(encodeFrame({ id: 2, op: "render", page: "nope", props }));
  proc.stdin.flush();
  expect((await reader.next())!.header).toMatchObject({ id: 2, error: "unknown page nope" });

  proc.stdin.write(encodeFrame({ id: 3, op: "render", page: "hello", props }));
  proc.stdin.flush();
  const png = await reader.next();
  expect(png!.header.id).toBe(3);
  expect(png!.body!.length).toBe(png!.header.png as number);

  proc.stdin.end();
  expect(await proc.exited).toBe(0);
}, 20000);

test("a throwing page with stray output yields an error frame and the process keeps serving", async () => {
  const proc = Bun.spawn(["bun", join(root, "renderer/main.ts"), "--pages", join(root, "renderer/testdata/pages")], {
    stdin: "pipe",
    stdout: "pipe",
    stderr: "pipe",
  });
  const reader = new FrameReader(proc.stdout);
  const props = { data: {}, viewport: { width: 100, height: 100, portrait: false } };

  for (const id of [1, 2]) {
    proc.stdin.write(encodeFrame({ id, op: "render", page: "test/broken", props }));
    proc.stdin.flush();
    expect((await reader.next())!.header).toMatchObject({ id, error: "boom" });
  }

  proc.stdin.write(encodeFrame({ id: 3, op: "render", page: "test/broken", props: {} }));
  proc.stdin.flush();
  expect((await reader.next())!.header).toMatchObject({ id: 3, error: "invalid render request" });

  proc.stdin.write(encodeFrame({ id: 4, op: "nope" }));
  proc.stdin.flush();
  expect((await reader.next())!.header).toMatchObject({ id: 4, error: "invalid nope request" });

  proc.stdin.write(encodeFrame({ id: 5, op: "describe" }));
  proc.stdin.flush();
  expect((await reader.next())!.header.pages).toEqual([
    { name: "test/broken", data: { forecast: { module: "weather", query: "forecast", params: { hours: 2 } } } },
  ]);

  proc.stdin.end();
  expect(await proc.exited).toBe(0);
  const stderr = await new Response(proc.stderr).text();
  expect(stderr).toContain("stray output");
  expect(stderr).toMatch(/rendering test\/broken failed:.*\n.*at Broken/);
}, 20000);
