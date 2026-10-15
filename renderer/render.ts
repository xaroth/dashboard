import type { Renderer } from "@takumi-rs/core";
import { createElement } from "react";
import { render } from "takumi-js";
import type { Viewport } from "../pages/lib/page";
import type { Page } from "./bundle";

export async function renderPage(
  renderer: Renderer,
  page: Page,
  viewport: Viewport,
  props: Record<string, unknown>,
): Promise<Uint8Array> {
  const element = createElement(page.component, { ...props, viewport });
  return render(element, { renderer, width: viewport.width, height: viewport.height, format: "png", css: page.css });
}
