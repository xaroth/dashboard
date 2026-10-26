import type { Renderer } from "@takumi-rs/core";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { render } from "takumi-js";
import type { Page, RenderProps } from "./bundle";

type Viewport = RenderProps["viewport"];

/**
 * The viewport page lays out in, and the scale from it to viewport. Shared
 * pages, and layout pages that export `scaled = true`, are written for the
 * E1001's 800x480 and scaled to fit the panel, so one set of px sizes works on
 * both panels; other layout pages get the panel as is.
 */
export function layout(page: Page, viewport: Viewport): { viewport: Viewport; scale: number } {
  if (!page.scaled) return { viewport, scale: 1 };
  const [w, h] = viewport.portrait ? [480, 800] : [800, 480];
  const scale = Math.min(viewport.width / w, viewport.height / h);
  return {
    viewport: { ...viewport, width: Math.round(viewport.width / scale), height: Math.round(viewport.height / scale) },
    scale,
  };
}

/** Renders page with props, at props.viewport. */
export async function renderPage(
  renderer: Renderer,
  page: Page,
  props: RenderProps,
): Promise<Uint8Array> {
  const { width, height } = props.viewport;
  const { viewport, scale } = layout(page, props.viewport);
  const element = createElement(page.component, { ...props, viewport });
  // takumi lays out in px of width / devicePixelRatio and draws at full resolution.
  return render(element, { renderer, width, height, devicePixelRatio: scale, format: "png", css: page.css });
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
  const { viewport, scale } = layout(page, props.viewport);
  const { width, height } = viewport;
  const markup = renderToStaticMarkup(createElement(page.component, { ...props, viewport }));
  return `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>${page.name}</title>
<style>${base()}
body { width: ${width}px; height: ${height}px;${scale !== 1 ? ` zoom: ${scale};` : ""} }</style>
<style>${page.css}</style>
</head>
<body>${markup}</body>
</html>
`;
}
