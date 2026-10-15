import { Glob } from "bun";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import type { ComponentType } from "react";
import type { Query } from "../pages/lib/page";

export type Page = {
  name: string;
  data: Record<string, Query>;
  component: ComponentType<any>;
  css: string;
};

/** Bundles every top-level `pages/*.tsx` into its own entry and loads it. */
export async function bundlePages(dir: string): Promise<Map<string, Page>> {
  const entrypoints = [...new Glob("*.tsx").scanSync({ cwd: dir, absolute: true })].sort();
  const outdir = await mkdtemp(join(tmpdir(), "dashboard-pages-"));
  try {
    const result = await Bun.build({ entrypoints, outdir, target: "bun", naming: "[name].[ext]", throw: false });
    if (!result.success) throw new Error(result.logs.map(String).join("\n"));

    const pages = new Map<string, Page>();
    for (const entry of entrypoints) {
      const name = basename(entry, ".tsx");
      const js = join(outdir, `${name}.js`);
      const cssFile = Bun.file(join(outdir, `${name}.css`));
      const css = (await cssFile.exists()) ? await cssFile.text() : "";

      // A fresh path per build, so a rebuild is not served from the module cache.
      const mod = await import(`${js}?v=${Date.now()}`);
      if (typeof mod.default !== "function") throw new Error(`${name}: default export is not a component`);
      pages.set(name, { name, data: mod.data ?? {}, component: mod.default, css });
    }
    return pages;
  } finally {
    await rm(outdir, { recursive: true, force: true });
  }
}
