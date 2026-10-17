import { expect, test } from "bun:test";
import type { EveonlineSkillsCharacter } from "../generated/queries";
import { skillLevel, sortQueues } from "./eveonline";

test("skill levels as roman numerals", () => {
  expect([1, 2, 3, 4, 5].map(skillLevel)).toEqual(["I", "II", "III", "IV", "V"]);
});

test("queues sorted by urgency", () => {
  const c = (name: string, ends: string | null, paused = false): EveonlineSkillsCharacter => ({
    id: 0, name, paused, ends, remaining: 0, skills: [], totalSp: 0, unallocatedSp: 0, fetchedAt: null, error: "",
  });
  const sorted = sortQueues([
    c("Paused", null, true),
    c("Later", "2026-10-20T00:00:00Z"),
    c("Empty", null),
    c("Soon", "2026-10-18T00:00:00Z"),
  ]);
  expect(sorted.map((x) => x.name)).toEqual(["Empty", "Soon", "Later", "Paused"]);
});
