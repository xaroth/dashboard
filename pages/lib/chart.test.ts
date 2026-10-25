import { expect, test } from "bun:test";
import { linear, paddedRange, runs, svgPoints, yScale } from "./chart";

test("linear maps domain onto range", () => {
  const s = linear(10, 20, 0, 100);
  expect([s(10), s(15), s(20), s(25)]).toEqual([0, 50, 100, 150]);
  expect(linear(0, 1, 100, 0)(0.25)).toBe(75);
});

test("y scale puts max at the top, inset by half the stroke", () => {
  const y = yScale(-1, 1, 104, 4);
  expect([y(1), y(0), y(-1)]).toEqual([2, 52, 102]);
});

test("svg points to 0.1 px", () => {
  expect(svgPoints([[1, 2.04], [3.25, 4]])).toBe("1.0,2.0 3.3,4.0");
  expect(svgPoints([])).toBe("");
});

test("padded range", () => {
  expect(paddedRange([0, 10], 0.15, 0.5)).toEqual([-1.5, 11.5]);
  expect(paddedRange([3, 3], 0.15, 0.5)).toEqual([2.5, 3.5]);
});

test("runs break at gaps", () => {
  const p = (m: number, v: number | null) => ({ t: `2026-10-25T12:0${m}:00Z`, v });
  const got = runs([p(0, null), p(1, 1), p(2, 2), p(3, null), p(4, 4)]);
  expect(got.map((r) => r.map((x) => x.v))).toEqual([[1, 2], [4]]);
  expect(got[0]![0]!.t).toBe(Date.parse("2026-10-25T12:01:00Z"));
  expect(runs([])).toEqual([]);
});
