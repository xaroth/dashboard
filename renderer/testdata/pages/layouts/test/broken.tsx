import { query } from "../../../../../pages/lib/page";

export const data = { forecast: query("weather.forecast", { hours: 2 }) };

export default function Broken() {
  process.stdout.write("stray output\n");
  console.log("log output");
  throw new Error("boom");
}
