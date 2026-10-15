import { expect, test } from "bun:test";
import { formatDateTime } from "./format";

test("Dutch date in the configured timezone", () => {
  expect(formatDateTime("2026-10-16T05:30:00Z", "Europe/Amsterdam")).toBe("vrijdag 16 oktober om 07:30");
});
