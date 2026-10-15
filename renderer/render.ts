import type { Renderer } from "@takumi-rs/core";
import { createElement } from "react";
import { render } from "takumi-js";
import type { Viewport } from "../pages/lib/page";
import type { Page } from "./bundle";

/** Renders page with props, at props.viewport. */
export async function renderPage(
  renderer: Renderer,
  page: Page,
  props: Record<string, unknown> & { viewport: Viewport },
): Promise<Uint8Array> {
  const { viewport } = props;
  const element = createElement(page.component, props);
  return render(element, { renderer, width: viewport.width, height: viewport.height, format: "png", css: page.css });
}
