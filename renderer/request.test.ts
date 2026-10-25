import { expect, test } from "bun:test";
import { isRequest } from "./request";

test("request guard", () => {
  const viewport = { width: 800, height: 480, portrait: false };
  const cases: [Record<string, unknown>, boolean][] = [
    [{ id: 1, op: "describe" }, true],
    [{ id: 1, op: "render", page: "e1001/hello", props: { data: {}, viewport } }, true],
    [{ op: "describe" }, false],
    [{ id: "1", op: "describe" }, false],
    [{ id: 1, op: "nope" }, false],
    [{ id: 1, op: "render", props: { data: {}, viewport } }, false],
    [{ id: 1, op: "render", page: "p" }, false],
    [{ id: 1, op: "render", page: "p", props: [] }, false],
    [{ id: 1, op: "render", page: "p", props: { data: {} } }, false],
    [{ id: 1, op: "render", page: "p", props: { data: {}, viewport: { width: "800", height: 480 } } }, false],
    [{ id: 1, op: "render", page: "p", props: { viewport } }, false],
  ];
  for (const [h, want] of cases) expect(isRequest(h)).toBe(want);
});
