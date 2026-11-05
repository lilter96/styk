import {
  knownOrderField,
  sameOrderScope,
  type OrderLine,
  type OrderScope,
} from "./OrdersTypes";
import type { NativeDemandWork } from "./OrdersNativeTypes";

// This is only a candidate list. The backend proves no application and checks
// the entire event history again before approving or dispatching a new intent.
export function nativeRetryCandidate(
  work: NativeDemandWork,
  posting: string,
  scope: OrderScope,
) {
  const movement = work.command.native_movement;
  const receipt = work.receipt;
  const applied = movement && knownOrderField(movement.applicable);
  if (
    !sameOrderScope(work.scope, scope) ||
    !sameOrderScope(work.command.scope, scope) ||
    work.command.posting_number !== posting ||
    (work.state !== "REJECTED" &&
      !(work.state === "BLOCKED" && work.dispatch_count === 0)) ||
    !movement ||
    !["demand_create", "salesreturn_create"].includes(work.command.action) ||
    (applied !== true && applied !== false) ||
    !Array.isArray(movement.lines) ||
    !movement.lines.length ||
    receipt.http_status === 409 ||
    !["", "REJECTED", "BLOCKED"].includes(receipt.state ?? "")
  )
    return false;
  if (work.dispatch_count === 0) return true;
  return (
    work.dispatch_count === 1 &&
    receipt.state === "REJECTED" &&
    receipt.code === "PROVIDER_REJECTED" &&
    [400, 401, 403, 404, 405, 412, 413, 415, 422, 429].includes(
      receipt.http_status ?? 0,
    ) &&
    !receipt.external_id
  );
}

export function nativeRetryQuantities(
  lines: OrderLine[],
  work: NativeDemandWork,
) {
  const selected: Record<number, string> = {};
  for (const [index, line] of lines.entries()) {
    const prior = work.command.native_movement?.lines.find(
      (old) =>
        old.identity.provider_line_id === line.identity.provider_line_id &&
        old.identity.sku === line.identity.sku &&
        old.identity.offer_id === line.identity.offer_id,
    );
    if (prior) selected[index] = prior.quantity;
  }
  return selected;
}

export function nativeRetryLocalTime(value: string | undefined) {
  const time = Date.parse(value ?? "");
  if (!Number.isFinite(time)) return "";
  return new Date(time - new Date(time).getTimezoneOffset() * 60000)
    .toISOString()
    .slice(0, -1);
}
