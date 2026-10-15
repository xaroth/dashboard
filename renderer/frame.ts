// Frames: 4-byte big-endian length, a JSON header of that length, then for
// headers with a `png` field that many raw bytes.

export type Frame = { header: Record<string, unknown>; body?: Uint8Array };

const maxHeader = 16 << 20;
// Matches maxPNG on the Go side.
const maxBody = 64 << 20;

export function encodeFrame(header: Record<string, unknown>, body?: Uint8Array): Uint8Array {
  const json = new TextEncoder().encode(JSON.stringify(body ? { ...header, png: body.length } : header));
  const out = new Uint8Array(4 + json.length + (body?.length ?? 0));
  new DataView(out.buffer).setUint32(0, json.length);
  out.set(json, 4);
  if (body) out.set(body, 4 + json.length);
  return out;
}

export class FrameReader {
  private reader: ReadableStreamDefaultReader<Uint8Array>;
  private buf = new Uint8Array(0);

  constructor(stream: ReadableStream<Uint8Array>) {
    this.reader = stream.getReader();
  }

  /** Returns the next frame, or null on a clean end of stream. */
  async next(): Promise<Frame | null> {
    const len = await this.read(4);
    if (!len) return null;
    const n = new DataView(len.buffer, len.byteOffset).getUint32(0);
    if (n > maxHeader) throw new Error(`frame header too large: ${n}`);
    const json = await this.read(n);
    if (!json) throw new Error("stream ended inside a frame");
    const header = JSON.parse(new TextDecoder().decode(json));
    if (typeof header !== "object" || header === null || Array.isArray(header)) {
      throw new Error("frame header is not an object");
    }
    if (header.png === undefined) return { header };
    if (!Number.isInteger(header.png) || header.png < 0 || header.png > maxBody) {
      throw new Error(`invalid frame body length: ${header.png}`);
    }
    const body = await this.read(header.png);
    if (!body) throw new Error("stream ended inside a frame body");
    return { header, body };
  }

  private async read(n: number): Promise<Uint8Array | null> {
    while (this.buf.length < n) {
      const { value, done } = await this.reader.read();
      if (done) {
        if (this.buf.length === 0) return null;
        throw new Error("stream ended inside a frame");
      }
      const next = new Uint8Array(this.buf.length + value.length);
      next.set(this.buf);
      next.set(value, this.buf.length);
      this.buf = next;
    }
    const out = this.buf.slice(0, n);
    this.buf = this.buf.subarray(n);
    return out;
  }
}
