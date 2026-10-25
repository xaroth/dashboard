import type { Props, Result } from "../generated/props";
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

export type { Device, PowerSource, Result, System, Viewport, Wake } from "../generated/props";

export type PageProps<D extends Record<string, Query> = Record<string, never>> = Props<{
  [K in keyof D]: D[K] extends Query<infer N> ? Result<NonNullable<Queries[N]["result"]>> : never;
}>;
