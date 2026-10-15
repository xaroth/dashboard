// A query descriptor: what a page's `data` export declares. The service resolves
// it and passes the result to the page under the same key.
export type Query<T = unknown> = { module: string; query: string; params: Record<string, unknown>; __result?: T };

export function query<T = unknown>(name: string, params: Record<string, unknown> = {}): Query<T> {
  const [module, q] = name.split(".", 2);
  if (!module || !q) throw new Error(`query name must be "module.query": ${name}`);
  return { module, query: q, params };
}

export type Result<T> = { value: T | null; fetchedAt: string | null; stale: boolean; error: string | null };

export type Viewport = { width: number; height: number; portrait: boolean };

export type Device = {
  name: string;
  model: string;
  rotation: number;
  // Headers from the device's latest request, absent in previews without a device.
  wake?: { source?: string; battery?: number; rssi?: number; firmware?: string; at?: string };
};

export type System = { staleModules: string[] };

export type PageProps<D extends Record<string, Query> = Record<string, never>> = {
  data: { [K in keyof D]: D[K] extends Query<infer T> ? Result<T> : never };
  device: Device;
  viewport: Viewport;
  system: System;
  now: string;
  timezone: string;
};
