import type { RenderProps } from "./bundle";

export type Request =
  | { id: number; op: "describe" }
  | { id: number; op: "render"; page: string; props: RenderProps };

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/** Checks the fields the renderer relies on; the rest of props goes to the page as Go sent it. */
export function isRequest(h: Record<string, unknown>): h is Request {
  if (typeof h.id !== "number") return false;
  switch (h.op) {
    case "describe":
      return true;
    case "render": {
      if (typeof h.page !== "string" || !isObject(h.props)) return false;
      const { viewport, data } = h.props;
      return isObject(viewport) && typeof viewport.width === "number" && typeof viewport.height === "number" && isObject(data);
    }
    default:
      return false;
  }
}
