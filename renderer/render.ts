import type { Renderer } from "@takumi-rs/core";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { render } from "takumi-js";
import type { Page, RenderProps } from "./bundle";

/** Renders page with props, at props.viewport. */
export async function renderPage(
  renderer: Renderer,
  page: Page,
  props: RenderProps,
): Promise<Uint8Array> {
  const { viewport } = props;
  const element = createElement(page.component, props);
  return render(element, { renderer, width: viewport.width, height: viewport.height, format: "png", css: page.css });
}

// Takumi's defaults: border-box sizing, no margins, and Geist as its built-in font.
const baseCSS = `*, *::before, *::after { box-sizing: border-box; margin: 0; }
body { overflow: hidden; font-family: Geist, system-ui, sans-serif; }`;

/**
 * Renders page with props as an HTML document of props.viewport's size, for
 * inspecting a page in a browser. Takumi lays out and draws text close to,
 * not exactly like, a browser; renderPage is what the device shows.
 */
export function renderHTML(page: Page, props: RenderProps): string {
  const { width, height } = props.viewport;
  const markup = renderToStaticMarkup(createElement(page.component, props));
  return `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>${page.name}</title>
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Geist:wght@100..900&display=swap">
<style>${baseCSS}
body { width: ${width}px; height: ${height}px; }</style>
<style>${page.css}</style>
</head>
<body>${markup}</body>
</html>
`;
}
