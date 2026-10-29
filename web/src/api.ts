export class ApiError extends Error {
  constructor(
    public status: number,
    public code?: string,
  ) {
    super(
      status === 401
        ? "Your session has expired. Sign in again."
        : "The requested data could not be loaded.",
    );
  }
}
export async function api<T>(path: string, signal?: AbortSignal): Promise<T> {
  const r = await fetch(path, { credentials: "same-origin", signal });
  if (!r.ok) throw await apiError(r);
  if (r.status === 204) return null as T;
  return r.json() as Promise<T>;
}
async function apiError(r: Response): Promise<ApiError> {
  let code: string | undefined;
  try {
    const value: unknown = await r.json();
    if (
      value &&
      typeof value === "object" &&
      "error" in value &&
      typeof value.error === "string"
    )
      code = value.error;
  } catch {
    /* Do not expose an untrusted error body. */
  }
  return new ApiError(r.status, code);
}
export async function post<T>(path: string, body: unknown): Promise<T> {
  const r = await fetch(path, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!r.ok) throw await apiError(r);
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
  observed_at?: string;
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

// One intent owns both the key and the exact serialized request. Keep it in
// memory until the outcome is known; a retry must reuse this object.
export type ConfirmationIntent = Readonly<{
  reviewId: string;
  body: string;
  kind: "product" | "warehouse";
}>;
export function createConfirmationIntent(
  reviewId: string,
  explanation: string,
  kind: "product" | "warehouse" = "product",
): ConfirmationIntent {
  if (!/^[0-9a-f]{32}$/.test(reviewId))
    throw new Error("Invalid review identifier.");
  if (
    new TextEncoder().encode(explanation).length > 1000 ||
    explanation.includes("\0")
  )
    throw new Error("Explanation is invalid or too long.");
  const key = crypto.randomUUID();
  return Object.freeze({
    reviewId,
    kind,
    body: JSON.stringify({
      idempotency_key: key,
      acknowledge: true,
      explanation,
    }),
  });
}
export type ConfirmationResult = {
  mapping_id: number;
  mapping_version: number;
  review_id: string;
  evidence_class: string;
  confirmed_at: string;
  idempotent_replay: boolean;
};
export async function sendConfirmation(
  intent: ConfirmationIntent,
  signal?: AbortSignal,
): Promise<ConfirmationResult> {
  const response = await fetch(
    `/api/${intent.kind}-mapping-reviews/${intent.reviewId}/confirm`,
    {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: intent.body,
      signal,
    },
  );
  if (!response.ok) {
    throw await apiError(response);
  }
  return response.json() as Promise<ConfirmationResult>;
}
