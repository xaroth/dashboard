import type { Queries } from "../generated/queries";

export type QueryName = keyof Queries;

// A query descriptor: what a page's `data` export declares. The service resolves
// it and passes the result to the page under the same key.
export type Query<K extends QueryName = QueryName> = {
  module: string;
  query: string;
  params: Queries[K]["params"];
  /** Type-level only: which query this is. */
  __name?: K;
};

/** Declares a query; a wrong name, param or param type is a type error. */
export function query<K extends QueryName>(name: K, params: Queries[K]["params"] = {}): Query<K> {
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
  wake?: {
    source?: string;
    battery?: number;
    rssi?: number;
    firmware?: string;
    at?: string;
    usb?: boolean;
    powerSource?: "header" | "config" | "default";
  };
};

export type System = { staleModules: string[] };

export type PageProps<D extends Record<string, Query> = Record<string, never>> = {
  data: { [K in keyof D]: D[K] extends Query<infer N> ? Result<NonNullable<Queries[N]["result"]>> : never };
  device: Device;
  viewport: Viewport;
  system: System;
  now: string;
  timezone: string;
};
