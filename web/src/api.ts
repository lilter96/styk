export class ApiError extends Error {
  constructor(public status: number) {
    super(
      status === 401
        ? "Your session has expired. Sign in again."
        : "The requested data could not be loaded.",
    );
  }
}
export async function api<T>(path: string, signal?: AbortSignal): Promise<T> {
  const r = await fetch(path, { credentials: "same-origin", signal });
  if (!r.ok) throw new ApiError(r.status);
  if (r.status === 204) return null as T;
  return r.json() as Promise<T>;
}
export type Account = {
  source: string;
  account: string;
  registered_at: string;
};
export type Batch = {
  present: boolean;
  completed: boolean;
  age: string;
  counts?: Record<string, number>;
};
export type Report = {
  checks: { code: string; status: string; detail: string }[];
  batches: Record<string, Batch>;
  decision_status: string;
  decision_reasons: string[];
  publication: string;
  mapping_counts: Record<string, number>;
};
export type Product = {
  product_id: string;
  offer_id: string;
  sku: string | null;
  archived: boolean | null;
  target_kind: string | null;
  target_id: string | null;
  match_status: string;
  candidate_count: number;
  candidates: { kind: string; id: string; evidence: string[] }[];
  candidates_truncated: boolean;
};
export type Warehouse = {
  id: string;
  name: string;
  status: string;
  is_rfbs: boolean | null;
  stock_rows: number;
  mapped: boolean | null;
};
export type Stock = {
  kind: string;
  item_id: string;
  stock: string | null;
  reserve: string | null;
  incoming: string | null;
  quantity: string | null;
  present: string | null;
  reserved: string | null;
  free_stock: string | null;
  observed_at: string;
};
export type Page<T> = { items: T[]; offset: number; has_next: boolean };
export const params = (p: Record<string, string | number>) =>
  new URLSearchParams(
    Object.entries(p).map(([k, v]) => [k, String(v)]),
  ).toString();
