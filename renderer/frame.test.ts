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

test("malformed header is an error", async () => {
  for (const [header, want] of [
    ["null", "not an object"],
    ['{"png":-1}', "body length"],
    ['{"png":1.5}', "body length"],
    [`{"png":${(64 << 20) + 1}}`, "body length"],
  ]) {
    const json = new TextEncoder().encode(header);
    const f = new Uint8Array(4 + json.length);
    new DataView(f.buffer).setUint32(0, json.length);
    f.set(json, 4);
    await expect(new FrameReader(streamOf(f, 1000)).next()).rejects.toThrow(want);
  }
});

// Shared with render/frame_internal_test.go.
const vectorRequest = "000000187b226964223a312c226f70223a226465736372696265227d";
const vectorResponse = "0000001b7b226964223a322c2268617368223a2268222c22706e67223a337d010203";

test("byte vectors shared with the Go side", async () => {
  expect(Buffer.from(encodeFrame({ id: 2, hash: "h" }, new Uint8Array([1, 2, 3]))).toString("hex")).toBe(vectorResponse);
  const r = new FrameReader(streamOf(new Uint8Array(Buffer.from(vectorRequest, "hex")), 1000));
  expect(await r.next()).toEqual({ header: { id: 1, op: "describe" } });
});
