import { Glob } from "bun";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { ComponentType } from "react";
import type { Props, Result } from "../pages/generated/props";
import type { Query } from "../pages/lib/page";

/** Props as Go sends them; each page narrows data to its own queries. */
export type RenderProps = Props<Record<string, Result<unknown>>>;

export type Page = {
  name: string;
  /** From shared/: laid out for 800x480 and scaled up to larger panels. */
  shared: boolean;
  data: Record<string, Query>;
  component: ComponentType<RenderProps>;
  css: string;
};

/**
 * Bundles every page under dir into its own entry and loads it: a layout's
 * `layouts/<layout>/<page>.tsx` named `<layout>/<page>`, a shared
 * `shared/<page>.tsx` named `<page>`.
 */
export async function bundlePages(dir: string): Promise<Map<string, Page>> {
  const files = ["layouts/*/*.tsx", "shared/*.tsx"].flatMap((p) => [...new Glob(p).scanSync({ cwd: dir })]).sort();
  const entrypoints = files.map((file) => join(dir, file));
  const outdir = await mkdtemp(join(tmpdir(), "dashboard-pages-"));
  try {
    const result = await Bun.build({ entrypoints, outdir, root: dir, target: "bun", naming: "[dir]/[name].[ext]", throw: false });
    if (!result.success) throw new Error(result.logs.map(String).join("\n"));

    const pages = new Map<string, Page>();
    for (const file of files) {
      const out = file.slice(0, -".tsx".length);
      const shared = file.startsWith("shared/");
      const name = out.slice((shared ? "shared/" : "layouts/").length);
      const js = join(outdir, `${out}.js`);
      const cssFile = Bun.file(join(outdir, `${out}.css`));
      const css = (await cssFile.exists()) ? await cssFile.text() : "";

      // A fresh path per build, so a rebuild is not served from the module cache.
      const mod = await import(`${js}?v=${Date.now()}`);
      if (typeof mod.default !== "function") throw new Error(`${name}: default export is not a component`);
      pages.set(name, { name, shared, data: mod.data ?? {}, component: mod.default, css });
    }
    return pages;
  } finally {
    await rm(outdir, { recursive: true, force: true });
  }
}
