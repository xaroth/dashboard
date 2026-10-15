import { query } from "../../../pages/lib/page";

export const data = { power: query("p1.power", { range: "24h" }) };

export default function Broken() {
  process.stdout.write("stray output\n");
  console.log("log output");
  throw new Error("boom");
}
