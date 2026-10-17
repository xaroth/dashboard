import type { EveonlineSkillsCharacter } from "../generated/queries";

const roman = ["0", "I", "II", "III", "IV", "V"];

/** Skill level as in game: 1 to 5 as I to V. */
export function skillLevel(level: number): string {
  return roman[level] ?? String(level);
}

/** Most urgent first: empty queues, then by when they end, paused last; ties by name. */
export function sortQueues(chars: EveonlineSkillsCharacter[]): EveonlineSkillsCharacter[] {
  const rank = (c: EveonlineSkillsCharacter) => (c.paused ? Infinity : c.ends ? Date.parse(c.ends) : -Infinity);
  return [...chars].sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
}
