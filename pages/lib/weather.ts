// WMO weather interpretation codes, as Open-Meteo reports them.
const labels: Record<number, string> = {
  0: "Onbewolkt",
  1: "Overwegend helder",
  2: "Half bewolkt",
  3: "Bewolkt",
  45: "Mist",
  48: "Mist met rijp",
  51: "Lichte motregen",
  53: "Motregen",
  55: "Dichte motregen",
  56: "Lichte ijzel",
  57: "IJzel",
  61: "Lichte regen",
  63: "Regen",
  65: "Zware regen",
  66: "Lichte ijzelregen",
  67: "IJzelregen",
  71: "Lichte sneeuw",
  73: "Sneeuw",
  75: "Zware sneeuw",
  77: "Korrelsneeuw",
  80: "Lichte buien",
  81: "Buien",
  82: "Zware buien",
  85: "Sneeuwbuien",
  86: "Zware sneeuwbuien",
  95: "Onweer",
  96: "Onweer met hagel",
  99: "Zwaar onweer met hagel",
};

export function weatherLabel(code: number): string {
  return labels[code] ?? `Code ${code}`;
}

const directions = ["N", "NO", "O", "ZO", "Z", "ZW", "W", "NW"];

/** Compass direction the wind comes from, in Dutch abbreviations. */
export function windDirection(degrees: number): string {
  return directions[Math.round((((degrees % 360) + 360) % 360) / 45) % 8]!;
}
