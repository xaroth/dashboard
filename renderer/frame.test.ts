import { expect, test } from "bun:test";
import { encodeFrame, FrameReader } from "./frame";

function streamOf(bytes: Uint8Array, chunk: number): ReadableStream<Uint8Array> {
  let i = 0;
  return new ReadableStream({
    pull(c) {
      if (i >= bytes.length) return c.close();
      c.enqueue(bytes.slice(i, i + chunk));
      i += chunk;
    },
  });
}

test("round trip across chunk boundaries", async () => {
  const png = new Uint8Array([137, 80, 78, 71, 0, 1, 2]);
  const a = encodeFrame({ id: 1, op: "describe" });
  const b = encodeFrame({ id: 2 }, png);
  const all = new Uint8Array([...a, ...b]);
  for (const chunk of [1, 3, 1000]) {
    const r = new FrameReader(streamOf(all, chunk));
    expect(await r.next()).toEqual({ header: { id: 1, op: "describe" } });
    expect(await r.next()).toEqual({ header: { id: 2, png: png.length }, body: png });
    expect(await r.next()).toBeNull();
  }
});

test("length prefix is big-endian", () => {
  const f = encodeFrame({});
  expect([...f.slice(0, 4)]).toEqual([0, 0, 0, 2]);
});

test("truncated frame is an error", async () => {
  const f = encodeFrame({ id: 1 });
  const r = new FrameReader(streamOf(f.slice(0, f.length - 1), 1000));
  expect(r.next()).rejects.toThrow("ended inside");
});
