import type { Renderer } from "@takumi-rs/core";
import { readFileSync } from "node:fs";
import { join } from "node:path";
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

// Takumi's defaults: border-box sizing, no margins, and its built-in font.
// fonts/Geist.woff2 is that font, extracted from @takumi-rs/core 2.16.2: Geist
// 1.500, weights 300-800, subset to the characters takumi ships.
let baseCSS: string | undefined;
function base(): string {
  if (baseCSS === undefined) {
    const font = readFileSync(join(import.meta.dir, "fonts/Geist.woff2")).toString("base64");
    baseCSS = `@font-face { font-family: Geist; font-weight: 300 800; src: url(data:font/woff2;base64,${font}) format("woff2"); }
*, *::before, *::after { box-sizing: border-box; margin: 0; }
body { overflow: hidden; font-family: Geist, system-ui, sans-serif; }`;
  }
  return baseCSS;
}

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
<style>${base()}
body { width: ${width}px; height: ${height}px; }</style>
<style>${page.css}</style>
</head>
<body>${markup}</body>
</html>
`;
}
