import { Glob } from "bun";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { ComponentType } from "react";
import type { Query } from "../pages/lib/page";

export type Page = {
  name: string;
  data: Record<string, Query>;
  component: ComponentType<any>;
  css: string;
};

/**
 * Bundles every `layouts/<layout>/<page>.tsx` under dir into its own entry and
 * loads it, named `<layout>/<page>`.
 */
export async function bundlePages(dir: string): Promise<Map<string, Page>> {
  const names = [...new Glob("layouts/*/*.tsx").scanSync({ cwd: dir })]
    .map((file) => file.slice("layouts/".length, -".tsx".length))
    .sort();
  const entrypoints = names.map((name) => join(dir, "layouts", `${name}.tsx`));
  const outdir = await mkdtemp(join(tmpdir(), "dashboard-pages-"));
  try {
    const root = join(dir, "layouts");
    const result = await Bun.build({ entrypoints, outdir, root, target: "bun", naming: "[dir]/[name].[ext]", throw: false });
    if (!result.success) throw new Error(result.logs.map(String).join("\n"));

    const pages = new Map<string, Page>();
    for (const name of names) {
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
