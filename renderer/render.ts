import { Renderer } from "@takumi-rs/core";
import { Glob } from "bun";
import { createElement } from "react";
import { render } from "takumi-js";
import type { Viewport } from "../pages/lib/page";
import type { Page } from "./bundle";

export async function loadRenderer(fontsDir: string): Promise<Renderer> {
  const renderer = new Renderer();
  for (const file of new Glob("*.{ttf,otf,woff2}").scanSync({ cwd: fontsDir, absolute: true })) {
    await renderer.registerFont(await Bun.file(file).bytes());
  }
  return renderer;
}

export async function renderPage(
  renderer: Renderer,
  page: Page,
  viewport: Viewport,
  props: Record<string, unknown>,
): Promise<Uint8Array> {
  const element = createElement(page.component, { ...props, viewport });
  return render(element, { renderer, width: viewport.width, height: viewport.height, format: "png", css: page.css });
}
