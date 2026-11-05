import {
  sameOrderScope,
  type NativeScope,
  type OrderScope,
  type OrdersPostingPage,
} from "./OrdersTypes";

export type OrderTargetState = {
  scope: NativeScope;
  posting_number: string;
  has_operation: boolean;
  state?: string;
  unresolved: boolean;
  customer_order_id?: string;
  source_changed: boolean;
  checked_at: string;
};
export type CurrentPostingPage = OrdersPostingPage & {
  target_states?: OrderTargetState[];
};

// Absence means an older API, not a successful synchronization or a zero fence.
export function validateTargetStates(
  page: CurrentPostingPage,
  scope: OrderScope,
) {
  if (page.target_states === undefined) return;
  const numbers = new Set((page.postings ?? []).map((p) => p.posting_number));
  if (
    !Array.isArray(page.target_states) ||
    page.target_states.length !== numbers.size
  )
    throw new Error("incomplete current posting target states");
  for (const item of page.target_states) {
    if (
      !sameOrderScope(item.scope, scope) ||
      !numbers.delete(item.posting_number) ||
      typeof item.unresolved !== "boolean" ||
      typeof item.has_operation !== "boolean" ||
      typeof item.source_changed !== "boolean" ||
      !Number.isFinite(Date.parse(item.checked_at))
    )
      throw new Error("invalid current posting target state");
  }
}
